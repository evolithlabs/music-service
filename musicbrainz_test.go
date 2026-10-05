package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api.groovio/catalog"
	"api.groovio/downloader"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func TestQueueAPIAndFileRanges(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	app.Settings().Logs.MaxDays = 0
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	// Occupy the worker slot so queueing cannot cause any external downloads.
	worker := &downloadWorker{app: app, slots: make(chan struct{}, 1)}
	worker.slots <- struct{}{}
	registerCatalogRoutes(app, &core.ServeEvent{App: app, Router: router}, catalog.NewClient("Groovio/test"), worker)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, byteRange string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, r)
		return result
	}
	invalid := request("POST", "/api/queue-track", `{"recordingId":"bad"}`, "")
	if invalid.Code != 400 {
		t.Fatalf("invalid queue response %d", invalid.Code)
	}
	payload := `{"recordingId":"026fa041-3917-4c73-9079-ed16e36f20f8","releaseId":"383be31c-37a0-4e08-8cda-cbcbbc587ae5","title":"Song"}`
	queued := request("POST", "/api/queue-track", payload, "")
	if queued.Code != 202 {
		t.Fatalf("queue response %d: %s", queued.Code, queued.Body)
	}
	var job downloader.Job
	if err := json.Unmarshal(queued.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" || job.RequestedReleaseID == "" {
		t.Fatal("queue acknowledgment lost request identity")
	}
	before := request("GET", "/api/downloads/"+job.ID+"/file", "", "")
	if before.Code != 404 {
		t.Fatal("exposed unfinished audio")
	}
	track, err := app.FindRecordById("tracks", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := filesystem.NewFileFromBytes([]byte("0123456789"), "sample.mp3")
	if err != nil {
		t.Fatal(err)
	}
	track.Set("file", file)
	track.Set("download_status", "completed")
	if err := app.Save(track); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ value, expected string }{{"bytes=2-4", "234"}, {"bytes=-3", "789"}} {
		result := request("GET", "/api/downloads/"+job.ID+"/file", "", item.value)
		if result.Code != http.StatusPartialContent || result.Body.String() != item.expected {
			t.Fatalf("range %s: %d %s", item.value, result.Code, result.Body)
		}
	}
	status := request("GET", "/api/downloads/"+job.ID, "", "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"status":"completed"`) {
		t.Fatal("missing completed status")
	}
}

func TestWorkerResolvesAndStoresTaggedAudio(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"026fa041-3917-4c73-9079-ed16e36f20f8","title":"Resolved title","artist-credit":[{"name":"Resolved artist","artist":{"id":"artist"}}],"isrcs":["GBAHT1600302"]}`))
	}))
	defer server.Close()
	client := catalog.NewClient("Groovio/test")
	client.BaseURL = server.URL
	client.Interval = time.Millisecond
	// Substitute a local audio fixture for yt-dlp. No YouTube traffic is made.
	toolsDir := t.TempDir()
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\nif [ \"$1\" = --output ]; then shift; target=$1; fi\nshift\ndone\nprintf 'ID3\\003\\000\\000\\000\\000\\000\\000\\377\\373\\220\\144' > \"$target\"\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "yt-dlp"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(t.TempDir())
	track, err := downloader.QueueTrack(app, downloader.DownloadRequest{RecordingID: "026fa041-3917-4c73-9079-ed16e36f20f8", Title: "Provisional title"})
	if err != nil {
		t.Fatal(err)
	}
	worker := &downloadWorker{app: app, catalog: client, slots: make(chan struct{}, 1)}
	worker.process()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		saved, err := app.FindRecordById("tracks", track.Id)
		if err != nil {
			t.Fatal(err)
		}
		if saved.GetString("download_status") == "failed" {
			t.Fatal(saved.GetString("download_error"))
		}
		if saved.GetString("download_status") == "completed" && len(worker.slots) == 0 {
			worker.tasks.Wait()
			if saved.GetString("name") != "Resolved title" || saved.GetString("artist") != "Resolved artist" || saved.GetString("file") == "" {
				t.Fatal("worker did not persist authoritative metadata and audio")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not complete the queued job")
}
