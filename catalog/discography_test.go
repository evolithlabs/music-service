package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArtistDiscographyIdentityRankingAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/artist":
			w.Write([]byte(`{"data":[{"id":2,"name":"NF Cover Band"},{"id":1,"name":"NF"}]}`))
		case "/artist/1":
			w.Write([]byte(`{"id":1,"name":"NF","picture_big":"https://artist.example/nf.jpg"}`))
		case "/artist/1/top":
			if r.URL.Query().Get("limit") != "12" {
				t.Error("top songs did not request twelve ranked recordings")
			}
			w.Write([]byte(`{"total":20,"data":[{"id":10,"title":"Popular song","duration":248,"artist":{"id":1,"name":"NF"},"album":{"id":3,"title":"Album"}}],"next":"next"}`))
		case "/album/3/tracks":
			w.Write([]byte(`{"total":3,"data":[{"id":20,"title":"Other artist","artist":{"id":2,"name":"NF Cover Band"}},{"id":21,"title":"NF song","artist":{"id":1,"name":"NF"}},{"id":21,"title":"Duplicate","artist":{"id":1,"name":"NF"}}]}`))
		case "/artist/1/albums":
			if r.URL.Query().Get("index") == "0" {
				w.Write([]byte(`{"total":1,"data":[{"id":3,"title":"Album","cover_big":"https://cover.example/album.jpg"}]}`))
				return
			}
			w.Write([]byte(`{"total":43,"data":[{"id":3,"title":"Album","cover_big":"https://cover.example/album.jpg","release_date":"2024-01-01"}],"next":"next"}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewDeezerClient()
	client.BaseURL = server.URL
	ctx := context.Background()
	artist, err := client.FindArtist(ctx, "NF", 0)
	if err != nil || artist.ID != 1 || artist.PictureURL == "" {
		t.Fatalf("wrong artist identity: %+v %v", artist, err)
	}
	if _, err := client.FindArtist(ctx, "Unknown NF", 0); err == nil {
		t.Fatal("silently selected a different artist")
	}
	top, err := client.ArtistTracks(ctx, *artist, 0, true)
	if err != nil || len(top.Tracks) != 1 || top.Tracks[0].DeezerArtistID != 1 || top.Tracks[0].DeezerAlbumID != 3 {
		t.Fatalf("missing discovery identities: %+v %v", top, err)
	}
	songs, err := client.ArtistTracks(ctx, *artist, 0, false)
	if err != nil || len(songs.Tracks) != 1 || songs.Tracks[0].DeezerID != 21 || songs.NextOffset != nil {
		t.Fatalf("incomplete discography, duplicates, or another artist: %+v %v", songs, err)
	}
	albums, err := client.ArtistAlbums(ctx, *artist, 20)
	if err != nil || len(albums.Albums) != 1 || albums.Albums[0].ArtistID != 1 || albums.NextOffset == nil || *albums.NextOffset != 21 {
		t.Fatalf("wrong album shelf: %+v %v", albums, err)
	}
}

func TestArtistSongsPageAcrossReleasesWithoutUsingSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/artist/1/albums" {
			w.Write([]byte(`{"data":[{"id":3,"title":"First album"},{"id":4,"title":"Second album"}],"total":2}`))
			return
		}
		start := 1
		if r.URL.Path == "/album/4/tracks" {
			start = 16
		} else if r.URL.Path != "/album/3/tracks" {
			t.Fatalf("unexpected discography request %s", r.URL)
		}
		items := []deezerTrack{}
		for id := start; id < start+15; id++ {
			item := deezerTrack{ID: int64(id), Title: "Song"}
			item.Artist.ID = 1
			item.Artist.Name = "Artist"
			items = append(items, item)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": items, "total": 15})
	}))
	defer server.Close()
	client := NewDeezerClient()
	client.BaseURL = server.URL
	artist := Artist{ID: 1, Name: "Artist"}
	first, err := client.ArtistTracks(context.Background(), artist, 0, false)
	if err != nil || len(first.Tracks) != 20 || first.NextOffset == nil || *first.NextOffset != 20 {
		t.Fatalf("bad first page %+v %v", first, err)
	}
	last, err := client.ArtistTracks(context.Background(), artist, *first.NextOffset, false)
	if err != nil || len(last.Tracks) != 10 || last.NextOffset != nil || last.Tracks[0].DeezerID != 21 || last.Count != 30 {
		t.Fatalf("lost remainder of second release: %+v %v", last, err)
	}
}

func TestAlbumTracksPreserveOrderCoverAndIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/album/3" {
			w.Write([]byte(`{"id":3,"title":"Album","cover_big":"https://cover.example/album.jpg","nb_tracks":2,"artist":{"id":1,"name":"NF"}}`))
			return
		}
		if r.URL.Path != "/album/3/tracks" || r.URL.Query().Get("index") != "50" {
			t.Errorf("wrong album request %s", r.URL)
		}
		w.Write([]byte(`{"total":52,"data":[{"id":11,"title":"First","duration":100,"artist":{"id":1,"name":"NF"}},{"id":12,"title":"Second","duration":200,"artist":{"id":9,"name":"Guest"}}]}`))
	}))
	defer server.Close()
	client := NewDeezerClient()
	client.BaseURL = server.URL
	album, err := client.Album(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.AlbumTracks(context.Background(), *album, 50)
	if err != nil || len(page.Tracks) != 2 || page.NextOffset != nil {
		t.Fatalf("wrong final album page: %+v %v", page, err)
	}
	if page.Tracks[0].DeezerID != 11 || page.Tracks[1].Artist != "Guest" || page.Tracks[1].DeezerAlbumID != 3 || page.Tracks[1].CoverURL != album.CoverURL {
		t.Fatal("lost ordering, album artwork, or guest artist")
	}
}
