package main

import (
	"strconv"
	"strings"

	"api.groovio/catalog"
	"github.com/pocketbase/pocketbase/core"
)

func catalogOffset(e *core.RequestEvent) (int, error) {
	value := e.Request.URL.Query().Get("offset")
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 || offset > 10000 {
		return 0, e.BadRequestError("Invalid collection offset", nil)
	}
	return offset, nil
}

func registerDiscographyRoutes(se *core.ServeEvent, client *catalog.DeezerClient) {
	se.Router.GET("/api/artist", func(e *core.RequestEvent) error {
		name := strings.TrimSpace(e.Request.URL.Query().Get("name"))
		if name == "" || len(name) > 1000 {
			return e.BadRequestError("Artist name is required", nil)
		}
		trackID, _ := strconv.ParseInt(e.Request.URL.Query().Get("trackId"), 10, 64)
		artist, err := client.FindArtist(e.Request.Context(), name, trackID)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not find this artist in the catalog. Please try again."})
		}
		return e.JSON(200, artist)
	})
	se.Router.GET("/api/artists/{id}/tracks", func(e *core.RequestEvent) error {
		id, _ := strconv.ParseInt(e.Request.PathValue("id"), 10, 64)
		offset, err := catalogOffset(e)
		if err != nil {
			return err
		}
		artist, err := client.Artist(e.Request.Context(), id)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load this artist."})
		}
		page, err := client.ArtistTracks(e.Request.Context(), *artist, offset, e.Request.URL.Query().Get("top") == "1")
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load artist songs. Please try again."})
		}
		return e.JSON(200, page)
	})
	se.Router.GET("/api/artists/{id}/albums", func(e *core.RequestEvent) error {
		id, _ := strconv.ParseInt(e.Request.PathValue("id"), 10, 64)
		offset, err := catalogOffset(e)
		if err != nil {
			return err
		}
		artist, err := client.Artist(e.Request.Context(), id)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load this artist."})
		}
		page, err := client.ArtistAlbums(e.Request.Context(), *artist, offset)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load albums. Please try again."})
		}
		return e.JSON(200, page)
	})
	se.Router.GET("/api/album", func(e *core.RequestEvent) error {
		query := e.Request.URL.Query()
		id, _ := strconv.ParseInt(query.Get("id"), 10, 64)
		var album *catalog.Album
		var err error
		if id > 0 {
			album, err = client.Album(e.Request.Context(), id)
		} else {
			name, artist := strings.TrimSpace(query.Get("name")), strings.TrimSpace(query.Get("artist"))
			if name == "" || len(name) > 1000 || len(artist) > 1000 {
				return e.BadRequestError("Album name is required", nil)
			}
			trackID, _ := strconv.ParseInt(query.Get("trackId"), 10, 64)
			album, err = client.FindAlbum(e.Request.Context(), name, artist, trackID)
		}
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not find this album in the catalog. Please try again."})
		}
		return e.JSON(200, album)
	})
	se.Router.GET("/api/albums/{id}/tracks", func(e *core.RequestEvent) error {
		id, _ := strconv.ParseInt(e.Request.PathValue("id"), 10, 64)
		offset, err := catalogOffset(e)
		if err != nil {
			return err
		}
		album, err := client.Album(e.Request.Context(), id)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load this album."})
		}
		page, err := client.AlbumTracks(e.Request.Context(), *album, offset)
		if err != nil {
			return e.JSON(502, map[string]string{"error": "Could not load album songs. Please try again."})
		}
		return e.JSON(200, page)
	})
}
