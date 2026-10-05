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
	"unicode"
)

type Track struct {
	DeezerID       int64  `json:"deezerId,omitempty"`
	PreviewURL     string `json:"previewUrl,omitempty"`
	CatalogURL     string `json:"catalogUrl,omitempty"`
	Source         string `json:"source,omitempty"`
	RecordingID    string `json:"recordingId"`
	ReleaseID      string `json:"releaseId,omitempty"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	ArtistIDs      string `json:"artistIds,omitempty"`
	Album          string `json:"album,omitempty"`
	AlbumArtist    string `json:"albumArtist,omitempty"`
	AlbumArtistIDs string `json:"albumArtistIds,omitempty"`
	DurationMs     int    `json:"durationMs"`
	ReleaseDate    string `json:"releaseDate,omitempty"`
	TrackNumber    int    `json:"trackNumber,omitempty"`
	CoverURL       string `json:"coverUrl,omitempty"`
	ISRC           string `json:"isrc,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
}

type SearchResult struct {
	Tracks []Track `json:"tracks"`
	Count  int     `json:"count"`
	Offset int     `json:"offset"`
}

func identity(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

// Discovery stays on Deezer; MusicBrainz enriches the selected recording.
func (c *Client) Match(ctx context.Context, seed Track) (*Track, error) {
	queries := []string{}
	if seed.ISRC != "" {
		queries = append(queries, "isrc:"+seed.ISRC)
	}
	escape := func(value string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) }
	queries = append(queries, `recording:"`+escape(seed.Title)+`" AND artist:"`+escape(seed.Artist)+`"`)
	for _, query := range queries {
		var data struct {
			Recordings []recording `json:"recordings"`
		}
		if err := c.get(ctx, "/recording", url.Values{"query": {query}, "limit": {"25"}}, &data); err != nil {
			return nil, err
		}
		for _, item := range data.Recordings {
			artist, _ := artistCredit(item.Credits)
			if identity(item.Title) != identity(seed.Title) || identity(artist) != identity(seed.Artist) {
				continue
			}
			if seed.DurationMs > 0 && item.Length > 0 && abs(item.Length-seed.DurationMs) > 6000 {
				continue
			}
			selected := chooseRelease(item.Releases)
			for _, candidate := range item.Releases {
				if identity(candidate.Title) == identity(seed.Album) {
					selected = candidate
					break
				}
			}
			return c.Lookup(ctx, item.ID, selected.ID)
		}
	}
	return nil, nil
}
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

type credit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}
type release struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Date    string   `json:"date"`
	Status  string   `json:"status"`
	Credits []credit `json:"artist-credit"`
	Group   struct {
		PrimaryType string `json:"primary-type"`
	} `json:"release-group"`
	Media []struct {
		Tracks []struct {
			Position  int `json:"position"`
			Recording struct {
				ID string `json:"id"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}
type recording struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Length         int       `json:"length"`
	Disambiguation string    `json:"disambiguation"`
	Credits        []credit  `json:"artist-credit"`
	Releases       []release `json:"releases"`
	ISRCs          []string  `json:"isrcs"`
}
type cacheEntry struct {
	data    []byte
	expires time.Time
}

// One client is shared by all MusicBrainz enrichment lookups. The gate also
// serializes cache access, ensuring all requests from this service share a limit.
type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
	Interval  time.Duration
	gate      chan struct{}
	next      time.Time
	cache     map[string]cacheEntry
}

func NewClient(userAgent string) *Client {
	return &Client{BaseURL: "https://musicbrainz.org/ws/2", UserAgent: userAgent,
		HTTP: &http.Client{Timeout: 20 * time.Second}, Interval: 1100 * time.Millisecond,
		gate: make(chan struct{}, 1), cache: make(map[string]cacheEntry)}
}

func (c *Client) get(ctx context.Context, path string, values url.Values, result any) error {
	values.Set("fmt", "json")
	address := c.BaseURL + path + "?" + values.Encode()
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if entry, ok := c.cache[address]; ok && time.Now().Before(entry.expires) {
		return json.Unmarshal(entry.data, result)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if delay := time.Until(c.next); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		request.Header.Set("User-Agent", c.UserAgent)
		request.Header.Set("Accept", "application/json")
		c.next = time.Now().Add(c.Interval)
		response, err := c.HTTP.Do(request)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 5<<20))
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			delay := time.Duration(2<<attempt) * time.Second
			if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(date) > delay {
				delay = time.Until(date)
			}
			// Retain the upstream cooldown for subsequent callers, even if this
			// request is cancelled or has exhausted its retries.
			c.next = time.Now().Add(delay)
			if attempt < 2 {
				continue
			}
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
		}
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("invalid MusicBrainz response: %w", err)
		}
		if len(c.cache) >= 128 {
			clear(c.cache)
		}
		c.cache[address] = cacheEntry{data, time.Now().Add(10 * time.Minute)}
		return nil
	}
	return fmt.Errorf("MusicBrainz is temporarily unavailable")
}

func searchQuery(query string) string {
	terms := strings.Fields(query)
	for i, term := range terms {
		var escaped strings.Builder
		for _, char := range term {
			if strings.ContainsRune(`+-!(){}[]^"~*?:\/&|`, char) {
				escaped.WriteByte('\\')
			}
			escaped.WriteRune(char)
		}
		terms[i] = "(recording:" + escaped.String() + " OR artist:" + escaped.String() + ")"
	}
	return strings.Join(terms, " AND ")
}

func (c *Client) Search(ctx context.Context, query string, offset int) (*SearchResult, error) {
	var data struct {
		Count      int         `json:"count"`
		Recordings []recording `json:"recordings"`
	}
	err := c.get(ctx, "/recording", url.Values{"query": {searchQuery(query)}, "limit": {"20"}, "offset": {strconv.Itoa(offset)}}, &data)
	if err != nil {
		return nil, err
	}
	result := &SearchResult{Tracks: make([]Track, 0, len(data.Recordings)), Count: data.Count, Offset: offset}
	for _, item := range data.Recordings {
		result.Tracks = append(result.Tracks, normalize(item, chooseRelease(item.Releases)))
	}
	return result, nil
}

func (c *Client) Lookup(ctx context.Context, recordingID, releaseID string) (*Track, error) {
	var item recording
	if err := c.get(ctx, "/recording/"+recordingID, url.Values{"inc": {"artist-credits+releases+release-groups+isrcs"}}, &item); err != nil {
		return nil, err
	}
	selected := chooseRelease(item.Releases)
	if releaseID != "" {
		selected.ID = releaseID
	}
	if selected.ID != "" {
		var full release
		if err := c.get(ctx, "/release/"+selected.ID, url.Values{"inc": {"artist-credits+recordings+release-groups"}}, &full); err != nil {
			return nil, err
		}
		found := false
		for _, medium := range full.Media {
			for _, track := range medium.Tracks {
				if track.Recording.ID == recordingID {
					found = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("selected release does not contain this recording")
		}
		selected = full
	}
	track := normalize(item, selected)
	return &track, nil
}

func chooseRelease(releases []release) release {
	for _, item := range releases {
		if item.Status == "Official" && item.Group.PrimaryType == "Album" {
			return item
		}
	}
	for _, item := range releases {
		if item.Status == "Official" {
			return item
		}
	}
	if len(releases) > 0 {
		return releases[0]
	}
	return release{}
}

func artistCredit(credits []credit) (string, string) {
	var names strings.Builder
	ids := make([]string, 0, len(credits))
	for _, item := range credits {
		name := item.Name
		if name == "" {
			name = item.Artist.Name
		}
		names.WriteString(name + item.JoinPhrase)
		if item.Artist.ID != "" {
			ids = append(ids, item.Artist.ID)
		}
	}
	return names.String(), strings.Join(ids, ", ")
}

func normalize(item recording, album release) Track {
	artist, artistIDs := artistCredit(item.Credits)
	albumArtist, albumArtistIDs := artistCredit(album.Credits)
	if albumArtist == "" {
		albumArtist, albumArtistIDs = artist, artistIDs
	}
	track := Track{RecordingID: item.ID, ReleaseID: album.ID, Title: item.Title, Artist: artist, ArtistIDs: artistIDs,
		Album: album.Title, AlbumArtist: albumArtist, AlbumArtistIDs: albumArtistIDs, DurationMs: item.Length,
		ReleaseDate: album.Date, Disambiguation: item.Disambiguation}
	if len(item.ISRCs) > 0 {
		track.ISRC = item.ISRCs[0]
	}
	if album.ID != "" {
		track.CoverURL = "https://coverartarchive.org/release/" + album.ID + "/front-500"
	}
	for _, medium := range album.Media {
		for _, entry := range medium.Tracks {
			if entry.Recording.ID == item.ID {
				track.TrackNumber = entry.Position
			}
		}
	}
	return track
}
