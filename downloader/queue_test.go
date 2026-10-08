package downloader

import (
	"api.groovio/catalog"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "api.groovio/migrations"
	"github.com/bogem/id3v2"
	"github.com/pocketbase/pocketbase/core"
)

const testRecordingID = "026fa041-3917-4c73-9079-ed16e36f20f8"
const testReleaseID = "383be31c-37a0-4e08-8cda-cbcbbc587ae5"

func TestDeezerQueueIdentityAndEnrichmentFallback(t *testing.T) {
	app := testApp(t)
	first, err := QueueTrack(app, DownloadRequest{DeezerID: 716493312, Title: "Provisional", Artist: "Artist"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := QueueTrack(app, DownloadRequest{DeezerID: 716493312})
	if err != nil || first.Id != duplicate.Id {
		t.Fatalf("duplicate Deezer job: %v", err)
	}
	other, err := QueueTrack(app, DownloadRequest{DeezerID: 412536982})
	if err != nil || first.Id == other.Id {
		t.Fatalf("Deezer identities collapsed: %v", err)
	}
	deezerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":716493312,"title":"The Search","duration":248,"isrc":"USUM71907047","artist":{"name":"NF"},"album":{"title":"The Search","cover_big":"https://cover.example/nf.jpg"}}`))
	}))
	defer deezerServer.Close()
	mbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"recordings":[]}`)) }))
	defer mbServer.Close()
	mb := catalog.NewClient("Groovio/test")
	mb.BaseURL = mbServer.URL
	mb.Interval = time.Millisecond
	deezer := catalog.NewDeezerClient()
	deezer.BaseURL = deezerServer.URL
	if err := ResolveTrack(context.Background(), app, mb, first, deezer); err != nil {
		t.Fatal(err)
	}
	if first.GetString("name") != "The Search" || first.GetString("artist") != "NF" || first.GetInt("duration") != 248000 || first.GetString("metadata_source") != "deezer" || first.GetString("musicbrainz_recording_id") != "" {
		t.Fatal("unmatched metadata blocked authoritative Deezer metadata")
	}
	// Different Deezer editions can enrich to the same MusicBrainz recording.
	for _, record := range []*core.Record{first, other} {
		record.Set("musicbrainz_recording_id", testRecordingID)
		record.Set("musicbrainz_release_id", testReleaseID)
		if err := app.Save(record); err != nil {
			t.Fatal("enrichment identity incorrectly prevented distinct Deezer jobs:", err)
		}
	}
}

func testApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ResetBootstrapState() })
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestDurableQueueDeduplicationAndRetry(t *testing.T) {
	app := testApp(t)
	request := DownloadRequest{RecordingID: testRecordingID, ReleaseID: testReleaseID, Title: "Song", Artist: "Artist"}
	var group sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			record, err := QueueTrack(app, request)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- record.Id
		}()
	}
	group.Wait()
	close(ids)
	id := ""
	for value := range ids {
		if id != "" && id != value {
			t.Fatal("created duplicate jobs")
		}
		id = value
	}
	track, err := app.FindRecordById("tracks", id)
	if err != nil {
		t.Fatal(err)
	}
	if track.GetString("download_status") != "queued" || track.GetString("name") != "Song" {
		t.Fatal("request was not durably queued")
	}
	if track.GetInt("duration") != 0 {
		t.Fatal("queue unexpectedly resolved metadata")
	}
	track.Set("download_status", "failed")
	track.Set("download_error", "temporary failure")
	if err := app.Save(track); err != nil {
		t.Fatal(err)
	}
	track, err = QueueTrack(app, request)
	if err != nil || track.Id != id || track.GetString("download_status") != "queued" || track.GetString("download_error") != "" {
		t.Fatalf("retry failed: %v", err)
	}
	other, err := QueueTrack(app, DownloadRequest{RecordingID: testRecordingID})
	if err != nil || other.Id == id {
		t.Fatal("release identities collapsed")
	}
	if _, err := QueueTrack(app, DownloadRequest{RecordingID: "invalid"}); err == nil {
		t.Fatal("invalid MBID accepted")
	}
}

func TestEmbeddedProvenanceAndUnknownDuration(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{RecordingID: testRecordingID, ReleaseID: testReleaseID, Title: "Song", Artist: "Artist"})
	if err != nil {
		t.Fatal(err)
	}
	track.Set("album_id", testReleaseID)
	track.Set("deezer_id", 716493312)
	track.Set("metadata_source", "musicbrainz")
	track.Set("isrc", "USUM71907047")
	track.Set("name", "Song (remix)")
	args := strings.Join(createYTDLPCommand(context.Background(), track, "output.mp3").Args, " ")
	if strings.Contains(args, "--match-filter") {
		t.Fatal("unknown duration excludes every candidate")
	}
	if !strings.Contains(args, "Song (remix)") {
		t.Fatal("lost MusicBrainz recording version when searching audio")
	}
	path := filepath.Join(t.TempDir(), "sample.mp3")
	if err := os.WriteFile(path, []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 0, 0xff, 0xfb, 0x90, 0x64}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeID3Tags(track, path, "test", filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tag.Close()
	if tag.Title() != "Song (remix)" || tag.Artist() != "Artist" {
		t.Fatal("missing ID3 metadata")
	}
	values := map[string]string{}
	for _, frame := range tag.GetFrames("TXXX") {
		value := frame.(id3v2.UserDefinedTextFrame)
		values[value.Description] = value.Value
	}
	if values["MUSICBRAINZ_TRACKID"] != testRecordingID || values["GROOVIO_ORIGIN"] != "service" {
		t.Fatal("missing embedded provenance")
	}
	if values["DEEZER_TRACK_ID"] != "716493312" || values["GROOVIO_METADATA_SOURCE"] != "musicbrainz" || tag.GetTextFrame("TSRC").Text != "USUM71907047" {
		t.Fatal("missing Deezer provenance or ISRC")
	}
}

func TestPreparedAudioSizeAndDurableUploadFailure(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 716493312})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prepared.mp3")
	if err := os.WriteFile(path, make([]byte, 8<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := updateTrackRecord(app, track, path); err != nil {
		t.Fatal("normal MP3 size rejected:", err)
	}
	savedFile := track.GetString("file")
	if savedFile == "" {
		t.Fatal("prepared audio was not saved")
	}
	collection, err := app.FindCollectionByNameOrId("tracks")
	if err != nil {
		t.Fatal(err)
	}
	collection.Fields.GetByName("file").(*core.FileField).MaxSize = 32
	if err := app.Save(collection); err != nil {
		t.Fatal(err)
	}
	track, err = app.FindRecordById("tracks", track.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updateTrackRecord(app, track, path); err == nil {
		t.Fatal("oversized upload accepted")
	}
	if track.GetString("file") != savedFile {
		t.Fatal("rejected upload replaced the saved file")
	}
	track.Set("download_status", "failed")
	track.Set("download_error", "file rejected")
	if err := app.Save(track); err != nil {
		t.Fatal("failed upload prevented durable error state:", err)
	}
	reloaded, err := app.FindRecordById("tracks", track.Id)
	if err != nil || reloaded.GetString("download_status") != "failed" {
		t.Fatal("failure state not persisted:", err)
	}
}

func TestID3UnicodeMetadata(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 2580253682})
	if err != nil {
		t.Fatal(err)
	}
	cover := []byte{0xff, 0xd8, 0xff, 0xe0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(cover)
	}))
	defer server.Close()
	track.Set("cover_url", server.URL)
	track.Set("metadata_source", "deezer")
	for _, example := range []struct {
		field string
		frame string
		value string
	}{
		{"name", "TIT2", "Bling‐Bang‐Bang‐Born"},
		{"artist", "TPE1", "クリーピーナッツ"},
		{"album", "TALB", "Ősz 🎵"},
		{"album_artist", "TPE2", "宇多田ヒカル"},
	} {
		t.Run(example.field, func(t *testing.T) {
			for _, field := range []string{"name", "artist", "album", "album_artist"} {
				track.Set(field, "ASCII metadata")
			}
			track.Set(example.field, example.value)
			path := filepath.Join(t.TempDir(), "unicode.mp3")
			if err := os.WriteFile(path, []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 0, 0xff, 0xfb, 0x90, 0x64}, 0600); err != nil {
				t.Fatal(err)
			}
			if err := writeID3Tags(track, path, "unicode", filepath.Dir(path)); err != nil {
				t.Fatal("Unicode metadata prevented saving audio:", err)
			}
			tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tag.Close()
			frame := tag.GetTextFrame(example.frame)
			if tag.Version() != 3 || frame.Text != example.value || !frame.Encoding.Equals(id3v2.EncodingUTF16) {
				t.Fatalf("Unicode metadata did not round-trip: %+v", frame)
			}
			pictures := tag.GetFrames("APIC")
			if len(pictures) != 1 {
				t.Fatal("cover art was lost")
			}
			picture := pictures[0].(id3v2.PictureFrame)
			if !picture.Encoding.Equals(id3v2.EncodingUTF16) || string(picture.Picture) != string(cover) {
				t.Fatal("cover art does not use the Unicode-compatible tag encoding")
			}
		})
	}
}
