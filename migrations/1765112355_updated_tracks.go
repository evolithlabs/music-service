package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_327047008")
		if err != nil {
			return err
		}

		// add field
		if err := collection.Fields.AddMarshaledJSONAt(10, []byte(`{
			"hidden": false,
			"id": "select3120095287",
			"maxSelect": 1,
			"name": "download_status",
			"presentable": false,
			"required": false,
			"system": false,
			"type": "select",
			"values": [
				"queued",
				"downloading",
				"completed",
				"failed"
			]
		}`)); err != nil {
			return err
		}

		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("pbc_327047008")
		if err != nil {
			return err
		}

		// remove field
		collection.Fields.RemoveById("select3120095287")

		return app.Save(collection)
	})
}
