package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"api.groovio/catalog"
	"api.groovio/downloader"
	"github.com/pocketbase/pocketbase/core"
)

type downloadWorker struct {
	app     core.App
	catalog *catalog.Client
	deezer  *catalog.DeezerClient
	mu      sync.Mutex
	tasks   sync.WaitGroup
	slots   chan struct{}
}

func (w *downloadWorker) process() {
	w.mu.Lock()
	defer w.mu.Unlock()
	available := cap(w.slots) - len(w.slots)
	if available == 0 {
		return
	}
	tracks, err := w.app.FindRecordsByFilter("tracks", "download_status = 'queued'", "created", available, 0)
	if err != nil {
		log.Printf("Reading download queue: %v", err)
		return
	}
	for _, track := range tracks {
		w.slots <- struct{}{}
		track.Set("download_status", "resolving")
		if err := w.app.Save(track); err != nil {
			<-w.slots
			log.Printf("Claiming job: %v", err)
			continue
		}
		w.tasks.Add(1)
		go w.run(track)
	}
}

func (w *downloadWorker) run(track *core.Record) {
	defer func() { <-w.slots; w.process(); w.tasks.Done() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	err := downloader.ResolveTrack(ctx, w.app, w.catalog, track, w.deezer)
	if err == nil {
		track.Set("download_status", "downloading")
		err = w.app.Save(track)
	}
	if err == nil {
		_, err = downloader.DownloadTrack(ctx, w.app, track)
	}
	if err != nil {
		track.Set("download_status", "failed")
		track.Set("download_error", err.Error())
		log.Printf("Download job %s failed: %v", track.Id, err)
	} else {
		track.Set("download_status", "completed")
		track.Set("audio_last_used", time.Now().UnixMilli())
		track.Set("download_error", "")
	}
	if err := w.app.Save(track); err != nil {
		log.Printf("Saving job %s: %v", track.Id, err)
	}
}

func registerCatalogRoutes(app core.App, se *core.ServeEvent, client *catalog.Client, worker *downloadWorker) {
	if worker.deezer == nil {
		worker.deezer = catalog.NewDeezerClient()
	}
	registerDiscographyRoutes(se, worker.deezer)
	se.Router.GET("/api/search", func(e *core.RequestEvent) error {
		query := strings.TrimSpace(e.Request.URL.Query().Get("q"))
		if len(query) < 2 || len(query) > 200 {
			return e.JSON(400, map[string]string{"error": "Search must contain 2–200 characters"})
		}
		offset := 0
		if value := e.Request.URL.Query().Get("offset"); value != "" {
			var err error
			offset, err = strconv.Atoi(value)
			if err != nil || offset < 0 || offset > 10000 {
				return e.JSON(400, map[string]string{"error": "Invalid search offset"})
			}
		}
		ctx, cancel := context.WithTimeout(e.Request.Context(), 30*time.Second)
		defer cancel()
		result, err := worker.deezer.Search(ctx, query, offset)
		if err != nil {
			log.Printf("Deezer search: %v", err)
			return e.JSON(502, map[string]string{"error": "Deezer search is temporarily unavailable. Please try again."})
		}
		return e.JSON(200, result)
	})
	se.Router.POST("/api/queue-track", func(e *core.RequestEvent) error {
		var payload downloader.DownloadRequest
		if err := e.BindBody(&payload); err != nil {
			return e.JSON(400, map[string]string{"error": "Invalid download request"})
		}
		if err := payload.Validate(); err != nil {
			return e.JSON(400, map[string]string{"error": err.Error()})
		}
		track, err := downloader.QueueTrack(app, payload)
		if err != nil {
			log.Printf("Queueing recording %s: %v", payload.RecordingID, err)
			return e.JSON(500, map[string]string{"error": "Could not save the download request"})
		}
		go worker.process()
		return e.JSON(http.StatusAccepted, downloader.Snapshot(track))
	})
	se.Router.GET("/api/downloads/{id}", func(e *core.RequestEvent) error {
		track, err := app.FindRecordById("tracks", e.Request.PathValue("id"))
		if err != nil {
			return e.JSON(404, map[string]string{"error": "Download job not found"})
		}
		return e.JSON(200, downloader.Snapshot(track))
	})
	se.Router.GET("/api/downloads/{id}/file", func(e *core.RequestEvent) error {
		track, release, err := downloader.AcquireAudio(app, e.Request.PathValue("id"), time.Now())
		if err != nil {
			return e.JSON(404, map[string]string{"error": "Completed audio file not found"})
		}
		defer release()
		fs, err := app.NewFilesystem()
		if err != nil {
			return e.JSON(500, map[string]string{"error": "File storage unavailable"})
		}
		defer fs.Close()
		reader, err := fs.GetReader(track.BaseFilesPath() + "/" + track.GetString("file"))
		if err != nil {
			return e.JSON(404, map[string]string{"error": "Audio file not found"})
		}
		defer reader.Close()
		e.Response.Header().Set("Content-Type", "audio/mpeg")
		e.Response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.mp3"`, track.Id))
		http.ServeContent(e.Response, e.Request, track.GetString("file"), time.Time{}, reader)
		return nil
	})
}
