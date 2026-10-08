package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Artist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PictureURL string `json:"pictureUrl"`
}

type Album struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	ArtistID    int64  `json:"artistId"`
	CoverURL    string `json:"coverUrl"`
	TrackCount  int    `json:"trackCount"`
	ReleaseDate string `json:"releaseDate"`
}

type deezerArtist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PictureBig string `json:"picture_big"`
}

type deezerAlbum struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	CoverBig    string       `json:"cover_big"`
	TrackCount  int          `json:"nb_tracks"`
	ReleaseDate string       `json:"release_date"`
	Artist      deezerArtist `json:"artist"`
}

type TrackPage struct {
	Tracks     []Track `json:"tracks"`
	Count      int     `json:"count"`
	NextOffset *int    `json:"nextOffset"`
}

type AlbumPage struct {
	Albums     []Album `json:"albums"`
	Count      int     `json:"count"`
	NextOffset *int    `json:"nextOffset"`
}

func nextPage(offset, size int, next string) *int {
	if next == "" || size == 0 {
		return nil
	}
	value := offset + size
	return &value
}

func (c *DeezerClient) Artist(ctx context.Context, id int64) (*Artist, error) {
	var item deezerArtist
	if id <= 0 {
		return nil, fmt.Errorf("invalid artist ID")
	}
	if err := c.get(ctx, "/artist/"+strconv.FormatInt(id, 10), &item); err != nil {
		return nil, err
	}
	if item.ID != id || item.Name == "" {
		return nil, fmt.Errorf("artist not found")
	}
	return &Artist{ID: item.ID, Name: item.Name, PictureURL: item.PictureBig}, nil
}

func (c *DeezerClient) FindArtist(ctx context.Context, name string, trackID int64) (*Artist, error) {
	if trackID > 0 {
		track, err := c.Lookup(ctx, trackID)
		if err == nil && strings.EqualFold(strings.TrimSpace(track.Artist), strings.TrimSpace(name)) && track.DeezerArtistID > 0 {
			return c.Artist(ctx, track.DeezerArtistID)
		}
	}
	var data struct {
		Data []deezerArtist `json:"data"`
	}
	if err := c.get(ctx, "/search/artist?"+url.Values{"q": {name}, "limit": {"25"}}.Encode(), &data); err != nil {
		return nil, err
	}
	for _, item := range data.Data {
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(name)) {
			return c.Artist(ctx, item.ID)
		}
	}
	return nil, fmt.Errorf("artist not found")
}

func (c *DeezerClient) ArtistTracks(ctx context.Context, artist Artist, offset int, top bool) (*TrackPage, error) {
	if !top {
		return c.artistDiscography(ctx, artist, offset)
	}
	params := url.Values{"index": {strconv.Itoa(offset)}, "limit": {"20"}}
	path := "/artist/" + strconv.FormatInt(artist.ID, 10) + "/top?"
	params.Set("limit", "12")
	var data struct {
		Data  []deezerTrack `json:"data"`
		Total int           `json:"total"`
		Next  string        `json:"next"`
	}
	if err := c.get(ctx, path+params.Encode(), &data); err != nil {
		return nil, err
	}
	page := &TrackPage{Tracks: []Track{}, Count: data.Total, NextOffset: nextPage(offset, len(data.Data), data.Next)}
	for _, item := range data.Data {
		if item.ID > 0 && item.Title != "" && (top || item.Artist.ID == artist.ID) {
			page.Tracks = append(page.Tracks, normalizeDeezer(item))
		}
	}
	return page, nil
}

// Search results and the top endpoint are incomplete discographies. Walk the
// artist's releases, stopping as soon as this page and one lookahead song exist.
func (c *DeezerClient) artistDiscography(ctx context.Context, artist Artist, offset int) (*TrackPage, error) {
	if offset < 0 {
		return nil, fmt.Errorf("invalid song offset")
	}
	songs := []Track{}
	seen := make(map[int64]bool)
	for albumOffset := 0; ; {
		albums, err := c.ArtistAlbums(ctx, artist, albumOffset)
		if err != nil {
			return nil, err
		}
		for _, album := range albums.Albums {
			for songOffset := 0; ; {
				page, err := c.AlbumTracks(ctx, album, songOffset)
				if err != nil {
					return nil, err
				}
				for _, song := range page.Tracks {
					if seen[song.DeezerID] || song.DeezerArtistID != artist.ID {
						continue
					}
					seen[song.DeezerID] = true
					songs = append(songs, song)
					if len(songs) > offset+20 {
						next := offset + 20
						return &TrackPage{Tracks: songs[offset:next], Count: 0, NextOffset: &next}, nil
					}
				}
				if page.NextOffset == nil {
					break
				}
				songOffset = *page.NextOffset
			}
		}
		if albums.NextOffset == nil {
			break
		}
		albumOffset = *albums.NextOffset
	}
	return &TrackPage{Tracks: songs[min(offset, len(songs)):], Count: len(songs)}, nil
}

func normalizeAlbum(item deezerAlbum) Album {
	return Album{ID: item.ID, Title: item.Title, Artist: item.Artist.Name, ArtistID: item.Artist.ID, CoverURL: item.CoverBig, TrackCount: item.TrackCount, ReleaseDate: item.ReleaseDate}
}

func (c *DeezerClient) ArtistAlbums(ctx context.Context, artist Artist, offset int) (*AlbumPage, error) {
	var data struct {
		Data  []deezerAlbum `json:"data"`
		Total int           `json:"total"`
		Next  string        `json:"next"`
	}
	path := "/artist/" + strconv.FormatInt(artist.ID, 10) + "/albums?" + url.Values{"index": {strconv.Itoa(offset)}, "limit": {"20"}}.Encode()
	if err := c.get(ctx, path, &data); err != nil {
		return nil, err
	}
	page := &AlbumPage{Albums: []Album{}, Count: data.Total, NextOffset: nextPage(offset, len(data.Data), data.Next)}
	for _, item := range data.Data {
		if item.ID <= 0 {
			continue
		}
		album := normalizeAlbum(item)
		album.Artist = artist.Name
		album.ArtistID = artist.ID
		page.Albums = append(page.Albums, album)
	}
	return page, nil
}

func (c *DeezerClient) Album(ctx context.Context, id int64) (*Album, error) {
	var item deezerAlbum
	if id <= 0 {
		return nil, fmt.Errorf("invalid album ID")
	}
	if err := c.get(ctx, "/album/"+strconv.FormatInt(id, 10), &item); err != nil {
		return nil, err
	}
	if item.ID != id || item.Title == "" {
		return nil, fmt.Errorf("album not found")
	}
	album := normalizeAlbum(item)
	return &album, nil
}

func (c *DeezerClient) FindAlbum(ctx context.Context, name, artist string, trackID int64) (*Album, error) {
	if trackID > 0 {
		track, err := c.Lookup(ctx, trackID)
		if err == nil && strings.EqualFold(strings.TrimSpace(track.Album), strings.TrimSpace(name)) && (artist == "" || strings.EqualFold(strings.TrimSpace(track.Artist), strings.TrimSpace(artist))) && track.DeezerAlbumID > 0 {
			return c.Album(ctx, track.DeezerAlbumID)
		}
	}
	var data struct {
		Data []deezerAlbum `json:"data"`
	}
	if err := c.get(ctx, "/search/album?"+url.Values{"q": {artist + " " + name}, "limit": {"25"}}.Encode(), &data); err != nil {
		return nil, err
	}
	for _, item := range data.Data {
		if strings.EqualFold(strings.TrimSpace(item.Title), strings.TrimSpace(name)) && (artist == "" || strings.EqualFold(strings.TrimSpace(item.Artist.Name), strings.TrimSpace(artist))) {
			return c.Album(ctx, item.ID)
		}
	}
	return nil, fmt.Errorf("album not found")
}

func (c *DeezerClient) AlbumTracks(ctx context.Context, album Album, offset int) (*TrackPage, error) {
	var data struct {
		Data  []deezerTrack `json:"data"`
		Total int           `json:"total"`
		Next  string        `json:"next"`
	}
	path := "/album/" + strconv.FormatInt(album.ID, 10) + "/tracks?" + url.Values{"index": {strconv.Itoa(offset)}, "limit": {"50"}}.Encode()
	if err := c.get(ctx, path, &data); err != nil {
		return nil, err
	}
	page := &TrackPage{Tracks: []Track{}, Count: data.Total, NextOffset: nextPage(offset, len(data.Data), data.Next)}
	for _, item := range data.Data {
		if item.ID <= 0 || item.Title == "" {
			continue
		}
		track := normalizeDeezer(item)
		track.DeezerAlbumID = album.ID
		track.Album = album.Title
		track.CoverURL = album.CoverURL
		if track.Artist == "" {
			track.Artist = album.Artist
			track.DeezerArtistID = album.ArtistID
		}
		page.Tracks = append(page.Tracks, track)
	}
	return page, nil
}
