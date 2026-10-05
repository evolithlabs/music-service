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
		// PocketBase's default 5 MiB limit rejects ordinary high-quality MP3 songs.
		collection.Fields.GetByName("file").(*core.FileField).MaxSize = 100 << 20
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		collection.Fields.GetByName("file").(*core.FileField).MaxSize = 0
		return app.Save(collection)
	})
}
