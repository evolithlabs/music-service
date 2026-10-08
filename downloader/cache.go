package downloader

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// Queueing, access leases and eviction share a lock so an active response cannot
// lose its file, and a new request cannot reuse a record being expired.
var audioReaders = map[string]int{}

func AcquireAudio(app core.App, id string, now time.Time) (*core.Record, func(), error) {
	queueMutex.Lock()
	defer queueMutex.Unlock()
	track, err := app.FindRecordById("tracks", id)
	if err != nil || track.GetString("download_status") != "completed" || track.GetString("file") == "" {
		return nil, nil, fmt.Errorf("completed audio file not found")
	}
	track.Set("audio_last_used", now.UnixMilli())
	if err := app.Save(track); err != nil {
		return nil, nil, err
	}
	audioReaders[id]++
	return track, func() {
		queueMutex.Lock()
		defer queueMutex.Unlock()
		audioReaders[id]--
		if audioReaders[id] == 0 {
			delete(audioReaders, id)
		}
		// Start the inactivity window after the response finishes, too.
		if latest, err := app.FindRecordById("tracks", id); err == nil {
			latest.Set("audio_last_used", time.Now().UnixMilli())
			_ = app.Save(latest)
		}
	}, nil
}

func CleanupAudio(app core.App, now time.Time, ttl time.Duration, maxBytes int64) (int, error) {
	queueMutex.Lock()
	defer queueMutex.Unlock()
	tracks, err := app.FindRecordsByFilter("tracks", "download_status = 'completed' && file != ''", "", 0, 0)
	if err != nil {
		return 0, err
	}
	fs, err := app.NewFilesystem()
	if err != nil {
		return 0, err
	}
	defer fs.Close()
	type cached struct {
		track *core.Record
		used  time.Time
		size  int64
	}
	items := []cached{}
	var total int64
	for _, track := range tracks {
		used := time.UnixMilli(int64(track.GetFloat("audio_last_used")))
		if track.GetFloat("audio_last_used") == 0 {
			used = track.GetDateTime("updated").Time()
		}
		attributes, err := fs.Attributes(track.BaseFilesPath() + "/" + track.GetString("file"))
		if err != nil {
			if errors.Is(err, filesystem.ErrNotFound) {
				items = append(items, cached{track, now.Add(-ttl), 0})
				continue
			}
			return 0, err
		}
		total += attributes.Size
		items = append(items, cached{track, used, attributes.Size})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })
	removed := 0
	for _, item := range items {
		if audioReaders[item.track.Id] > 0 {
			continue
		}
		inactive := now.Sub(item.used)
		// A grace period gives newly prepared audio time to reach its requesting app.
		if inactive < ttl && (maxBytes <= 0 || total <= maxBytes || inactive < time.Hour) {
			continue
		}
		item.track.Set("file", "")
		item.track.Set("download_status", "expired")
		item.track.Set("download_error", "")
		if err := app.Save(item.track); err != nil {
			return removed, err
		}
		total -= item.size
		removed++
	}
	return removed, nil
}
