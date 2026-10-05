package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeezerRankedSearchPaginationAndCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if q.Get("q") != "NF" || q.Get("order") != "RANKING" || q.Get("index") != "20" || q.Get("limit") != "20" {
			t.Errorf("incorrect ranked query: %s", r.URL)
		}
		w.Write([]byte(`{"total":175,"data":[{"id":716493312,"title":"The Search","duration":248,"artist":{"name":"NF"},"album":{"title":"The Search","cover_big":"https://cover.example/nf.jpg"},"preview":"https://audio.example/preview.mp3","link":"https://deezer.com/track/716493312"}]}`))
	}))
	defer server.Close()
	client := NewDeezerClient()
	client.BaseURL = server.URL
	for i := 0; i < 2; i++ {
		result, err := client.Search(context.Background(), " NF ", 20)
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 175 || result.Offset != 20 || len(result.Tracks) != 1 {
			t.Fatalf("lost pagination: %+v", result)
		}
		track := result.Tracks[0]
		if track.DeezerID != 716493312 || track.Artist != "NF" || track.DurationMs != 248000 || track.CoverURL != "https://cover.example/nf.jpg" || track.RecordingID != "" || track.Source != "deezer" {
			t.Fatalf("incorrect normalized result: %+v", track)
		}
	}
	if requests != 1 {
		t.Fatalf("cache sent %d requests", requests)
	}
}

func TestDeezerRejectsErrorsAndMismatchedTrack(t *testing.T) {
	for _, payload := range []string{`{"error":{"message":"unavailable"}}`, `{"id":2,"title":"Song","artist":{"name":"Artist"}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(payload)) }))
		client := NewDeezerClient()
		client.BaseURL = server.URL
		if _, err := client.Lookup(context.Background(), 1); err == nil {
			t.Fatalf("accepted %s", payload)
		}
		server.Close()
	}
	client := NewDeezerClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Search(ctx, "NF", 0); err == nil {
		t.Fatal("ignored cancelled query")
	}
}

func TestMusicBrainzEnrichmentUsesISRCAndVerifiesIdentity(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		searches := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/recording" {
				searches++
				if searches == 1 && r.URL.Query().Get("query") != "isrc:USUM71907047" {
					t.Error("ISRC lookup was not first")
				}
				if searches == 2 && !strings.Contains(r.URL.Query().Get("query"), `artist:"NF"`) {
					t.Error("missing fallback artist constraint")
				}
				artist := "NF"
				length := "248000"
				if mismatch {
					artist = "Cover Artist"
					length = "400000"
				}
				w.Write([]byte(`{"recordings":[{"id":"nf-recording","title":"The Search","length":` + length + `,"artist-credit":[{"name":"` + artist + `"}],"releases":[]}]}`))
			} else {
				w.Write([]byte(`{"id":"nf-recording","title":"The Search","length":248000,"artist-credit":[{"name":"NF","artist":{"id":"nf-artist"}}],"isrcs":["USUM71907047"]}`))
			}
		}))
		client := NewClient("Groovio/test")
		client.BaseURL = server.URL
		client.Interval = time.Millisecond
		result, err := client.Match(context.Background(), Track{Title: "The Search", Artist: "NF", DurationMs: 248000, ISRC: "USUM71907047"})
		if err != nil {
			t.Fatal(err)
		}
		if mismatch && (result != nil || searches != 2) {
			t.Fatal("matched the wrong recording or skipped fallback")
		}
		if !mismatch && (result == nil || result.RecordingID != "nf-recording" || result.ArtistIDs != "nf-artist") {
			t.Fatalf("lost authoritative metadata: %+v", result)
		}
		server.Close()
	}
}
