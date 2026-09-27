/// <reference path="../pb_data/types.d.ts" />
migrate((app) => {
  const collection = app.findCollectionByNameOrId("pbc_327047008")

  // add field
  collection.fields.addAt(3, new Field({
    "autogeneratePattern": "",
    "hidden": false,
    "id": "text724990059",
    "max": 0,
    "min": 0,
    "name": "title",
    "pattern": "",
    "presentable": false,
    "primaryKey": false,
    "required": false,
    "system": false,
    "type": "text"
  }))

  // add field
  collection.fields.addAt(4, new Field({
    "autogeneratePattern": "",
    "hidden": false,
    "id": "text22648455",
    "max": 0,
    "min": 0,
    "name": "artist",
    "pattern": "",
    "presentable": false,
    "primaryKey": false,
    "required": false,
    "system": false,
    "type": "text"
  }))

  // add field
  collection.fields.addAt(5, new Field({
    "autogeneratePattern": "",
    "hidden": false,
    "id": "text966291011",
    "max": 0,
    "min": 0,
    "name": "album",
    "pattern": "",
    "presentable": false,
    "primaryKey": false,
    "required": false,
    "system": false,
    "type": "text"
  }))

  // add field
  collection.fields.addAt(6, new Field({
    "hidden": false,
    "id": "number2254405824",
    "max": null,
    "min": null,
    "name": "duration",
    "onlyInt": false,
    "presentable": false,
    "required": false,
    "system": false,
    "type": "number"
  }))

  return app.save(collection)
}, (app) => {
  const collection = app.findCollectionByNameOrId("pbc_327047008")

  // remove field
  collection.fields.removeById("text724990059")

  // remove field
  collection.fields.removeById("text22648455")

  // remove field
  collection.fields.removeById("text966291011")

  // remove field
  collection.fields.removeById("number2254405824")

  return app.save(collection)
})
