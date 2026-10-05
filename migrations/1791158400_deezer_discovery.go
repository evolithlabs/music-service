package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		collection.Fields.Add(&core.NumberField{Name: "deezer_id"})
		collection.Fields.Add(&core.TextField{Name: "metadata_source"})
		collection.RemoveIndex("idx_tracks_musicbrainz_identity")
		collection.AddIndex("idx_tracks_musicbrainz_identity", true, "musicbrainz_recording_id, musicbrainz_release_id", "musicbrainz_recording_id != '' AND deezer_id = 0")
		collection.AddIndex("idx_tracks_deezer", true, "deezer_id", "deezer_id > 0")
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		collection.Fields.RemoveByName("deezer_id")
		collection.Fields.RemoveByName("metadata_source")
		collection.RemoveIndex("idx_tracks_deezer")
		collection.RemoveIndex("idx_tracks_musicbrainz_identity")
		collection.AddIndex("idx_tracks_musicbrainz_identity", true, "musicbrainz_recording_id, musicbrainz_release_id", "musicbrainz_recording_id != ''")
		return app.Save(collection)
	})
}
