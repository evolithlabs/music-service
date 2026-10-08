package downloader

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func cachedAudio(t *testing.T, app core.App, id int64, used time.Time) *core.Record {
	t.Helper()
	track, err := QueueTrack(app, DownloadRequest{DeezerID: id, Title: "Keep metadata", Artist: "Artist"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := filesystem.NewFileFromBytes([]byte("0123456789"), "audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	track.Set("file", file)
	track.Set("download_status", "completed")
	track.Set("audio_last_used", used.UnixMilli())
	if err := app.Save(track); err != nil {
		t.Fatal(err)
	}
	return track
}

func TestAudioCacheExpiryDeletesBytesAndCanRequeue(t *testing.T) {
	app := testApp(t)
	now := time.Now()
	old := cachedAudio(t, app, 1, now.Add(-25*time.Hour))
	fresh := cachedAudio(t, app, 2, now.Add(-time.Hour))
	key := old.BaseFilesPath() + "/" + old.GetString("file")
	count, err := CleanupAudio(app, now, 24*time.Hour, 1<<30)
	if err != nil || count != 1 {
		t.Fatalf("expiry count %d: %v", count, err)
	}
	saved, err := app.FindRecordById("tracks", old.Id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.GetString("download_status") != "expired" || saved.GetString("file") != "" || saved.GetString("name") != "Keep metadata" {
		t.Fatal("expiry lost metadata or retained audio")
	}
	fs, err := app.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if exists, err := fs.Exists(key); err != nil || exists {
		t.Fatalf("expired bytes still stored: %v", err)
	}
	if current, _ := app.FindRecordById("tracks", fresh.Id); current.GetString("file") == "" {
		t.Fatal("deleted recent audio")
	}
	renewed, err := QueueTrack(app, DownloadRequest{DeezerID: 1})
	if err != nil || renewed.Id != old.Id || renewed.GetString("download_status") != "queued" {
		t.Fatalf("expired job was not reused: %v", err)
	}
}

func TestAudioCacheProtectsActiveResponsesAndTouchesAccess(t *testing.T) {
	app := testApp(t)
	now := time.Now()
	track := cachedAudio(t, app, 1, now.Add(-25*time.Hour))
	leased, release, err := AcquireAudio(app, track.Id, now)
	if err != nil {
		t.Fatal(err)
	}
	if leased.GetFloat("audio_last_used") < float64(now.Add(-time.Second).UnixMilli()) {
		t.Fatal("access did not refresh retention")
	}
	count, err := CleanupAudio(app, now.Add(25*time.Hour), 24*time.Hour, 1)
	if err != nil || count != 0 {
		t.Fatalf("deleted an active response: %d %v", count, err)
	}
	release()
	count, err = CleanupAudio(app, now.Add(25*time.Hour), 24*time.Hour, 1)
	if err != nil || count != 1 {
		t.Fatalf("released expired audio was not deleted: %d %v", count, err)
	}
}

func TestAudioCacheSizeEvictsOldestAndProtectsNewAudio(t *testing.T) {
	app := testApp(t)
	now := time.Now()
	oldest := cachedAudio(t, app, 1, now.Add(-5*time.Hour))
	older := cachedAudio(t, app, 2, now.Add(-4*time.Hour))
	newest := cachedAudio(t, app, 3, now.Add(-30*time.Minute))
	count, err := CleanupAudio(app, now, 24*time.Hour, 15)
	if err != nil || count != 2 {
		t.Fatalf("size eviction %d: %v", count, err)
	}
	for _, track := range []*core.Record{oldest, older} {
		saved, _ := app.FindRecordById("tracks", track.Id)
		if saved.GetString("download_status") != "expired" {
			t.Fatal("older file survived storage pressure")
		}
	}
	saved, _ := app.FindRecordById("tracks", newest.Id)
	if saved.GetString("file") == "" {
		t.Fatal("newly prepared file was evicted")
	}
}

func TestMissingAudioDoesNotBlockCleanupOrReuseDeadFiles(t *testing.T) {
	app := testApp(t)
	now := time.Now()
	missing := cachedAudio(t, app, 1, now)
	old := cachedAudio(t, app, 2, now.Add(-25*time.Hour))
	fs, err := app.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if err := fs.Delete(missing.BaseFilesPath() + "/" + missing.GetString("file")); err != nil {
		t.Fatal(err)
	}
	count, err := CleanupAudio(app, now, 24*time.Hour, 1<<30)
	if err != nil || count != 2 {
		t.Fatalf("missing file blocked cleanup: %d %v", count, err)
	}
	if record, _ := app.FindRecordById("tracks", old.Id); record.GetString("file") != "" {
		t.Fatal("old file was retained")
	}
	// Requeue also repairs absent files before the cleanup schedule runs.
	missing = cachedAudio(t, app, 3, now)
	if err := fs.Delete(missing.BaseFilesPath() + "/" + missing.GetString("file")); err != nil {
		t.Fatal(err)
	}
	queued, err := QueueTrack(app, DownloadRequest{DeezerID: 3})
	if err != nil || queued.GetString("download_status") != "queued" || queued.GetString("file") != "" {
		t.Fatalf("reused a missing file: %v", err)
	}
}
