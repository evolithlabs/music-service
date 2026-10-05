package catalog

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in contract check against the real public catalog; it never queues audio.
func TestMusicBrainzLive(t *testing.T) {
	if os.Getenv("GROOVIO_LIVE_TEST") != "1" {
		t.Skip("set GROOVIO_LIVE_TEST=1 to check the live MusicBrainz API")
	}
	client := NewClient("Groovio/0.1.0 (https://musicbrainz.org/user/FlyDoodle)")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	results, err := client.Search(ctx, "Daft Punk Get Lucky", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Tracks) == 0 {
		t.Fatal("no live search results")
	}
	track := results.Tracks[0]
	metadata, err := client.Lookup(ctx, track.RecordingID, track.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title == "" || metadata.Artist == "" || metadata.RecordingID == "" {
		t.Fatal("incomplete recording metadata")
	}
	t.Logf("Live catalog: %s — %s (%s)", metadata.Artist, metadata.Title, metadata.Album)
}
