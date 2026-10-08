package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type DeezerClient struct {
	BaseURL string
	HTTP    *http.Client
	gate    chan struct{}
	cache   map[string]cacheEntry
}

func NewDeezerClient() *DeezerClient {
	return &DeezerClient{BaseURL: "https://api.deezer.com", HTTP: &http.Client{Timeout: 15 * time.Second}, gate: make(chan struct{}, 1), cache: make(map[string]cacheEntry)}
}

type deezerTrack struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	ISRC     string `json:"isrc"`
	Preview  string `json:"preview"`
	Link     string `json:"link"`
	Artist   struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
	Album struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		CoverBig    string `json:"cover_big"`
		CoverMedium string `json:"cover_medium"`
	} `json:"album"`
}

func (c *DeezerClient) get(ctx context.Context, path string, result any) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if cached, ok := c.cache[path]; ok && time.Now().Before(cached.expires) {
		return json.Unmarshal(cached.data, result)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Deezer returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 3<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("Deezer: %s", envelope.Error.Message)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return err
	}
	if len(c.cache) >= 128 {
		clear(c.cache)
	}
	c.cache[path] = cacheEntry{data: data, expires: time.Now().Add(5 * time.Minute)}
	return nil
}
func normalizeDeezer(item deezerTrack) Track {
	cover := item.Album.CoverBig
	if cover == "" {
		cover = item.Album.CoverMedium
	}
	return Track{DeezerID: item.ID, DeezerArtistID: item.Artist.ID, DeezerAlbumID: item.Album.ID, Title: item.Title, Artist: item.Artist.Name, Album: item.Album.Title, DurationMs: item.Duration * 1000, CoverURL: cover, ISRC: item.ISRC, PreviewURL: item.Preview, CatalogURL: item.Link, Source: "deezer"}
}
func (c *DeezerClient) Search(ctx context.Context, query string, offset int) (*SearchResult, error) {
	params := url.Values{"q": {strings.TrimSpace(query)}, "index": {strconv.Itoa(offset)}, "limit": {"20"}, "order": {"RANKING"}}
	var data struct {
		Data  []deezerTrack `json:"data"`
		Total int           `json:"total"`
	}
	if err := c.get(ctx, "/search?"+params.Encode(), &data); err != nil {
		return nil, err
	}
	result := &SearchResult{Tracks: make([]Track, 0, len(data.Data)), Count: data.Total, Offset: offset}
	for _, item := range data.Data {
		if item.ID > 0 && item.Title != "" && item.Artist.Name != "" {
			result.Tracks = append(result.Tracks, normalizeDeezer(item))
		}
	}
	return result, nil
}
func (c *DeezerClient) Lookup(ctx context.Context, id int64) (*Track, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid Deezer track ID")
	}
	var item deezerTrack
	if err := c.get(ctx, "/track/"+strconv.FormatInt(id, 10), &item); err != nil {
		return nil, err
	}
	if item.ID != id || item.Title == "" || item.Artist.Name == "" {
		return nil, fmt.Errorf("invalid Deezer track response")
	}
	track := normalizeDeezer(item)
	return &track, nil
}
