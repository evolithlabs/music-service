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
		collection.Fields.Add(&core.NumberField{Name: "audio_last_used"})
		status := collection.Fields.GetByName("download_status").(*core.SelectField)
		status.Values = append(status.Values, "expired")
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tracks")
		if err != nil {
			return err
		}
		collection.Fields.RemoveByName("audio_last_used")
		status := collection.Fields.GetByName("download_status").(*core.SelectField)
		values := []string{}
		for _, value := range status.Values {
			if value != "expired" {
				values = append(values, value)
			}
		}
		status.Values = values
		return app.Save(collection)
	})
}
