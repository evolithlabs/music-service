package downloader

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudioSearchRetriesEmptyResultsWithoutDroppingRecordingVersion(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 1, Title: "Song (instrumental)", Artist: "Artist"})
	if err != nil {
		t.Fatal(err)
	}
	track.Set("metadata_source", "deezer")
	track.Set("duration", 90000)
	path := filepath.Join(t.TempDir(), "song.mp3")
	calls := 0
	err = downloadAudio(context.Background(), track, path, func(cmd *exec.Cmd) error {
		calls++
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "Song (instrumental)") || !strings.Contains(args, "duration>=80 & duration<=100") {
			t.Fatal("lost version or duration guard", args)
		}
		if calls == 1 {
			if !strings.Contains(args, "official audio") {
				t.Fatal("missing primary query")
			}
			return nil
		}
		if strings.Contains(args, "official audio") {
			t.Fatal("fallback repeated the rejected search")
		}
		return os.WriteFile(path, []byte("audio"), 0600)
	})
	if err != nil || calls != 2 {
		t.Fatalf("did not recover empty primary search: calls=%d err=%v", calls, err)
	}
}

func TestAudioSearchHonorsCancellationAndUnavailableRecordings(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 1, Title: "Song", Artist: "Artist"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err = downloadAudio(ctx, track, filepath.Join(t.TempDir(), "cancelled.mp3"), func(cmd *exec.Cmd) error { calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("retried cancelled playback")
	}
	err = downloadAudio(context.Background(), track, filepath.Join(t.TempDir(), "missing.mp3"), func(cmd *exec.Cmd) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "YouTube") {
		t.Fatal("reported a missing file as success")
	}
}
