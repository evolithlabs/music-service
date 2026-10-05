package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"strings"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		for _, name := range []string{"musicbrainz_recording_id", "musicbrainz_release_id", "isrc", "download_error"} {
			collection.Fields.Add(&core.TextField{Name: name, Max: 2000})
		}
		field := collection.Fields.GetByName("download_status").(*core.SelectField)
		field.Values = append(field.Values, "resolving")
		collection.Indexes = append(collection.Indexes, "CREATE UNIQUE INDEX idx_tracks_musicbrainz_identity ON tracks (musicbrainz_recording_id, musicbrainz_release_id) WHERE musicbrainz_recording_id != ''")
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		for _, name := range []string{"musicbrainz_recording_id", "musicbrainz_release_id", "isrc", "download_error"} {
			collection.Fields.RemoveByName(name)
		}
		indexes := collection.Indexes[:0]
		for _, index := range collection.Indexes {
			if !strings.Contains(index, "idx_tracks_musicbrainz_identity") {
				indexes = append(indexes, index)
			}
		}
		collection.Indexes = indexes
		collection.Fields.GetByName("download_status").(*core.SelectField).Values = []string{"queued", "downloading", "completed", "failed"}
		return app.Save(collection)
	})
}
