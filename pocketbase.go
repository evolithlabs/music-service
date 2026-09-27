package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"api.groovio/downloader"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	// For migrations to be auto run
	_ "api.groovio/migrations"
)

var downloadSemaphore = make(chan struct{}, 2)

func main() {
	app := pocketbase.New()

	// Detect if invoked via "go run .", so automigrate only runs in dev.
	isGoRun := strings.HasPrefix(os.Args[0], os.TempDir())

	// Register the migrate plugin. This adds the "migrate" subcommands to the CLI.
	// Automigrate: when true, changes from Dashboard will auto-generate migration files.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: isGoRun,
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {

		// 1. Queue the track for download
		se.Router.POST("/api/queue-track", func(e *core.RequestEvent) error {
			var payload downloader.DownloadRequest
			if err := e.BindBody(&payload); err != nil {
				return e.JSON(http.StatusBadRequest, map[string]string{
					"error": "Invalid request body: " + err.Error(),
				})
			}

			// Start the job in a background goroutine
			go func() {
				// The PocketBase app pointer is safe to use in a goroutine
				record, err := downloader.QueueTrack(app, payload)
				if err != nil {
					// Log the error for internal tracking
					log.Printf("Adding track to the queue FAILED for track ID %s: %v", payload.SpotifyTrackID, err)
					return // Job failed, nothing more to do for this background task.
				}
				
				// Optional: You could implement a webhook or a live-update mechanism (like websockets)
				// to notify the client when the record (and file) is ready.
				log.Printf("Track was succesfully added to the queue. Track ID: %s", record.Id)
			}()

			// Return an immediate success response indicating the job started.
			return e.JSON(http.StatusOK, map[string]string{
				"status": "Track was added to the queue",
			})
		})

		// 2. Register cron job for downloading queued tracks
		app.Cron().MustAdd("queue_worker", "*/1 * * * *", func() {
			// Find queued tracks
			tracks, err := app.FindRecordsByFilter(
				"tracks",
				"download_status='queued'",
				"-created",
				cap(downloadSemaphore),
				0,
			)
			if err != nil {
				// No rows returned
				return
			}

			for _, track := range tracks {
				// mark job as downloading
				track.Set("download_status", "downloading")
				app.Save(track)

				// fire worker
				go func(track *core.Record) {
					downloadSemaphore <- struct{}{} // acquire slot
					defer func() { <-downloadSemaphore }() // release slot

					_, err = downloader.DownloadTrack(app, track)
					if err != nil {
						track.Set("download_status", "failed")
						fmt.Printf("Failed to download track %s", err.Error())
					} else {
						track.Set("download_status", "completed")
					}

					if err := app.Save(track); err != nil {
						log.Println("Failed to update queued track status:", err)
					}

					app.Save(track)
				}(track)
			}
		})

		// 3. Expose endpoint for playing/download tracks
		se.Router.GET("/api/play-track/{spotifyTrackId}", func(e *core.RequestEvent) error {
			spotifyTrackId := e.Request.PathValue("spotifyTrackId")
			if spotifyTrackId == "" {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": "Missing spotifyTrackId"})
			}

			record, err := app.FindFirstRecordByData("tracks", "spotify_track_id", spotifyTrackId)
			if err != nil {
				return e.JSON(http.StatusNotFound, "Track not found")
			}

			fileName := record.GetString("file")
			if fileName == "" {
				return e.JSON(http.StatusNotFound, "Track file not available")
			}

			key := record.BaseFilesPath() + "/" + fileName

			fsys, err := app.NewFilesystem()
			if err != nil {
				return e.JSON(http.StatusInternalServerError, "Failed to initialize filesystem")
			}
			defer fsys.Close()

			// get file size
			stat, err := fsys.Attributes(key)
			if err != nil {
				return e.JSON(http.StatusNotFound, "File not found")
			}
			fileSize := stat.Size

			// Get reader (seekable)
			reader, err := fsys.GetReader(key)
			if err != nil {
				return e.JSON(http.StatusNotFound, "File not found")
			}
			defer reader.Close()

			rangeHeader := e.Request.Header.Get("Range")

			if rangeHeader != "" {
					if !strings.HasPrefix(rangeHeader, "bytes=") {
							return e.JSON(http.StatusBadRequest, "Invalid Range header")
					}

					// Strip "bytes="
					byteRange := strings.TrimPrefix(rangeHeader, "bytes=")

					// Support only single ranges (Chrome sometimes tries multiple)
					if strings.Contains(byteRange, ",") {
							// respond with full file (most players accept this)
							byteRange = strings.Split(byteRange, ",")[0]
					}

					parts := strings.Split(byteRange, "-")
					if len(parts) != 2 {
							return e.JSON(http.StatusBadRequest, "Invalid Range format")
					}

					// Parse start
					start, err := strconv.ParseInt(parts[0], 10, 64)
					if err != nil {
							start = 0
					}

					// Parse end (optional)
					var end int64
					if parts[1] == "" { // bytes=start-
							end = fileSize - 1
					} else {
							end, err = strconv.ParseInt(parts[1], 10, 64)
							if err != nil || end >= fileSize {
									end = fileSize - 1
							}
					}

					if start < 0 || start >= fileSize {
							return e.JSON(http.StatusRequestedRangeNotSatisfiable, "Invalid Range start")
					}

					chunkSize := end - start + 1
					if chunkSize < 1 {
							return e.JSON(http.StatusRequestedRangeNotSatisfiable, "Invalid Range")
					}

					// Seek
					_, err = reader.Seek(start, io.SeekStart)
					if err != nil {
							return e.JSON(500, "Seek failed")
					}

					// Headers
					e.Response.Header().Set("Content-Type", "audio/mpeg")
					e.Response.Header().Set("Accept-Ranges", "bytes")
					e.Response.Header().Set("Content-Length", fmt.Sprintf("%d", chunkSize))
					e.Response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))

					e.Response.WriteHeader(206)

					_, err = io.CopyN(e.Response, reader, chunkSize)
					return err
			}
			// No Range header → full file
			e.Response.Header().Set("Content-Type", "audio/mpeg")
			e.Response.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
			e.Response.Header().Set("Accept-Ranges", "bytes")

			_, err = io.Copy(e.Response, reader)
			return err
		})


		// 4. Expose endpoint for checking if tracks audio exist
		se.Router.GET("/api/check-tracks", func(e *core.RequestEvent) error {
			trackIdsQuery := e.Request.URL.Query().Get("ids")
			trackIds := strings.Split(trackIdsQuery, ",")

			// Build filter
			filters := make([]string, len(trackIds))
			params := make(dbx.Params)
			for i, id := range trackIds {
					key := fmt.Sprintf("id%d", i)
					filters[i] = "spotify_track_id = {:" + key + "}"
					params[key] = strings.TrimSpace(id)
			}

			// Seperate filters with OR operator
			filter := strings.Join(filters, " || ")

			// Fetch tracks
			tracks, _ := app.FindRecordsByFilter(
					"tracks",    // collection
					filter,      // filter string
					"",          // sort
					0,           // limit 0 = no limit
					0,           // offset
					params,
			)

			// Map tracks to spotify_track_id <-> download_status
			mappedTracks := make(map[string]string)
			for _, track := range tracks {
					status := track.GetString("download_status")
					if status != "" {
							spotifyTrackId := track.GetString("spotify_track_id")
							mappedTracks[spotifyTrackId] = status
					}
			}

			return e.JSON(http.StatusOK, mappedTracks)
		})

		// Serve static files from pb_public
		se.Router.GET("/{path...}", apis.Static(os.DirFS("./pb_public"), false))

		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
