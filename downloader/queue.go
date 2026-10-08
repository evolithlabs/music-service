package downloader

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"api.groovio/catalog"
	"github.com/google/uuid"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type DownloadRequest struct {
	DeezerID    int64  `json:"deezerId"`
	RecordingID string `json:"recordingId"`
	ReleaseID   string `json:"releaseId"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
}

func (request *DownloadRequest) Validate() error {
	if request.DeezerID < 0 {
		return fmt.Errorf("deezerId must be a positive number")
	}
	if request.DeezerID > 0 {
		// Only the authoritative lookup may assign a MusicBrainz identity to Deezer jobs.
		request.RecordingID = ""
		request.ReleaseID = ""
		if len(request.Title) > 1000 || len(request.Artist) > 1000 {
			return fmt.Errorf("title and artist must be at most 1000 bytes")
		}
		return nil
	}
	id, err := uuid.Parse(request.RecordingID)
	if err != nil {
		return fmt.Errorf("a valid MusicBrainz recordingId is required")
	}
	request.RecordingID = id.String()
	if request.ReleaseID != "" {
		id, err = uuid.Parse(request.ReleaseID)
		if err != nil {
			return fmt.Errorf("releaseId must be a MusicBrainz UUID")
		}
		request.ReleaseID = id.String()
	}
	if len(request.Title) > 1000 || len(request.Artist) > 1000 {
		return fmt.Errorf("title and artist must be at most 1000 bytes")
	}
	return nil
}

var queueMutex sync.Mutex

// Persist before acknowledging. MusicBrainz lookups only happen in the worker.
func QueueTrack(app core.App, request DownloadRequest) (*core.Record, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	queueMutex.Lock()
	defer queueMutex.Unlock()
	var result *core.Record
	err := app.RunInTransaction(func(tx core.App) error {
		filter := "musicbrainz_recording_id = {:recording} && musicbrainz_release_id = {:release} && deezer_id = 0"
		params := dbx.Params{"recording": request.RecordingID, "release": request.ReleaseID}
		if request.DeezerID > 0 {
			filter = "deezer_id = {:deezer}"
			params = dbx.Params{"deezer": request.DeezerID}
		}
		track, err := tx.FindFirstRecordByFilter("tracks", filter, params)
		if err == nil {
			if track.GetString("download_status") == "completed" && track.GetString("file") != "" {
				fs, err := tx.NewFilesystem()
				if err != nil {
					return err
				}
				exists, err := fs.Exists(track.BaseFilesPath() + "/" + track.GetString("file"))
				fs.Close()
				if err != nil {
					return err
				}
				if !exists {
					track.Set("file", "")
					track.Set("download_status", "expired")
				}
			}
			if track.GetString("download_status") == "failed" || track.GetString("download_status") == "expired" || track.GetString("download_status") == "completed" && track.GetString("file") == "" {
				track.Set("download_status", "queued")
				track.Set("download_error", "")
			}
			track.Set("audio_last_used", time.Now().UnixMilli())
			if err := tx.Save(track); err != nil {
				return err
			}
			result = track
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		collection, err := tx.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		track = core.NewRecord(collection)
		track.Set("deezer_id", request.DeezerID)
		track.Set("musicbrainz_recording_id", request.RecordingID)
		track.Set("musicbrainz_release_id", request.ReleaseID)
		track.Set("name", request.Title)
		track.Set("artist", request.Artist)
		track.Set("download_status", "queued")
		if err := tx.Save(track); err != nil {
			return err
		}
		result = track
		return nil
	})
	return result, err
}

func ResolveTrack(ctx context.Context, app core.App, client *catalog.Client, track *core.Record, deezerClients ...*catalog.DeezerClient) error {
	if id := int64(track.GetInt("deezer_id")); id > 0 {
		deezer := catalog.NewDeezerClient()
		if len(deezerClients) > 0 && deezerClients[0] != nil {
			deezer = deezerClients[0]
		}
		seed, err := deezer.Lookup(ctx, id)
		if err != nil {
			return err
		}
		metadata, matchErr := client.Match(ctx, *seed)
		track.Set("metadata_source", "deezer")
		if matchErr != nil || metadata == nil {
			metadata = seed
		} else {
			track.Set("metadata_source", "musicbrainz")
			track.Set("musicbrainz_recording_id", metadata.RecordingID)
			track.Set("musicbrainz_release_id", metadata.ReleaseID)
		}
		track.Set("name", metadata.Title)
		track.Set("artist", metadata.Artist)
		track.Set("album", seed.Album)
		track.Set("cover_url", seed.CoverURL)
		track.Set("duration", seed.DurationMs)
		track.Set("isrc", seed.ISRC)
		track.Set("album_id", metadata.ReleaseID)
		track.Set("artist_id", metadata.ArtistIDs)
		track.Set("album_artist", metadata.AlbumArtist)
		track.Set("album_artist_id", metadata.AlbumArtistIDs)
		track.Set("release_date", metadata.ReleaseDate)
		track.Set("track_number", metadata.TrackNumber)
		return app.Save(track)
	}
	if track.GetString("musicbrainz_recording_id") == "" {
		return nil
	} // Existing tagged Spotify records remain playable.
	metadata, err := client.Lookup(ctx, track.GetString("musicbrainz_recording_id"), track.GetString("musicbrainz_release_id"))
	if err != nil {
		return err
	}
	track.Set("name", metadata.Title)
	track.Set("metadata_source", "musicbrainz")
	track.Set("artist", metadata.Artist)
	track.Set("artist_id", metadata.ArtistIDs)
	track.Set("album", metadata.Album)
	track.Set("album_id", metadata.ReleaseID)
	track.Set("album_artist", metadata.AlbumArtist)
	track.Set("album_artist_id", metadata.AlbumArtistIDs)
	track.Set("cover_url", metadata.CoverURL)
	track.Set("duration", metadata.DurationMs)
	track.Set("release_date", metadata.ReleaseDate)
	track.Set("track_number", metadata.TrackNumber)
	track.Set("isrc", metadata.ISRC)
	return app.Save(track)
}

type Job struct {
	RequestedReleaseID string        `json:"requestedReleaseId"`
	ID                 string        `json:"id"`
	Status             string        `json:"status"`
	Error              string        `json:"error,omitempty"`
	Progress           *float64      `json:"progress,omitempty"`
	Stage              string        `json:"stage,omitempty"`
	Metadata           catalog.Track `json:"metadata"`
}

func Snapshot(track *core.Record) Job {
	releaseID := track.GetString("album_id")
	if releaseID == "" {
		releaseID = track.GetString("musicbrainz_release_id")
	}
	job := Job{ID: track.Id, RequestedReleaseID: track.GetString("musicbrainz_release_id"), Status: track.GetString("download_status"), Error: track.GetString("download_error"), Metadata: catalog.Track{
		DeezerID: int64(track.GetInt("deezer_id")), Source: track.GetString("metadata_source"),
		RecordingID: track.GetString("musicbrainz_recording_id"), ReleaseID: releaseID,
		Title: track.GetString("name"), Artist: track.GetString("artist"), Album: track.GetString("album"),
		DurationMs: track.GetInt("duration"), CoverURL: track.GetString("cover_url"), ISRC: track.GetString("isrc"),
	}}
	if job.Status == "downloading" {
		if value, ok := activeProgress.Load(track.Id); ok {
			progress := value.(downloadProgress)
			job.Progress, job.Stage = progress.Percent, progress.Stage
		}
	}
	return job
}
