package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSharedLimiterAndCache(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Groovio/test (https://musicbrainz.org/user/FlyDoodle)" {
			t.Error("missing application identity")
		}
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"count":0,"recordings":[]}`))
	}))
	defer server.Close()
	client := NewClient("Groovio/test (https://musicbrainz.org/user/FlyDoodle)")
	client.BaseURL = server.URL
	client.Interval = 30 * time.Millisecond
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func(offset int) {
			defer group.Done()
			if _, err := client.Search(context.Background(), "a song", offset); err != nil {
				t.Error(err)
			}
		}(i)
	}
	group.Wait()
	if _, err := client.Search(context.Background(), "a song", 0); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 4 {
		t.Fatalf("cache miss: got %d upstream requests", len(times))
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < 25*time.Millisecond {
			t.Fatal("concurrent callers bypassed shared limiter")
		}
	}
}

func TestCancelledWaitDoesNotContactUpstream(t *testing.T) {
	client := NewClient("Groovio/test")
	client.next = time.Now().Add(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	var result any
	if err := client.get(ctx, "/recording", url.Values{}, &result); err != context.DeadlineExceeded {
		t.Fatalf("got %v", err)
	}
}

func TestRetryAfterAndLookupValidation(t *testing.T) {
	requests := 0
	var first time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		if requests == 2 {
			if time.Since(first) < time.Second {
				t.Error("ignored upstream cooldown")
			}
			w.Write([]byte(`{"id":"recording","title":"Song","artist-credit":[{"name":"Artist","artist":{"id":"a"}}]}`))
		} else {
			w.Write([]byte(`{"id":"release","media":[{"tracks":[{"recording":{"id":"different-recording"}}]}]}`))
		}
	}))
	defer server.Close()
	client := NewClient("Groovio/test")
	client.BaseURL = server.URL
	client.Interval = time.Millisecond
	_, err := client.Lookup(context.Background(), "recording", "release")
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("accepted mismatched release: %v", err)
	}
}

func TestNormalizationAndQuery(t *testing.T) {
	credits := []credit{{Name: "First", JoinPhrase: " feat. "}, {Name: "Second"}}
	name, _ := artistCredit(credits)
	if name != "First feat. Second" {
		t.Fatal(name)
	}
	albums := []release{{ID: "single", Status: "Official"}, {ID: "album", Status: "Official"}}
	albums[1].Group.PrimaryType = "Album"
	if chooseRelease(albums).ID != "album" {
		t.Fatal("did not prefer official album")
	}
	query := searchQuery(`AC/DC title:foo`)
	if !strings.Contains(query, `artist:AC\/DC`) || !strings.Contains(query, `recording:title\:foo`) {
		t.Fatalf("unescaped search: %s", query)
	}
}
