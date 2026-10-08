package downloader

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLiveProgressHandlesSplitOutputAndProcessing(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 716493312})
	if err != nil {
		t.Fatal(err)
	}
	track.Set("download_status", "downloading")
	defer activeProgress.Delete(track.Id)
	output := &progressOutput{jobID: track.Id, output: io.Discard}
	for _, chunk := range []string{"[download] unrelated output\nGROOVIO_PRO", "GRESS:  23.4%\n"} {
		if _, err := output.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	job := Snapshot(track)
	if job.Progress == nil || *job.Progress != 23.4 || job.Stage != "downloading" {
		t.Fatalf("missing live progress: %+v", job)
	}
	for _, line := range []string{"GROOVIO_PROGRESS:NA", "GROOVIO_PROGRESS:NaN", "GROOVIO_PROGRESS:+Inf"} {
		updateProgress(track.Id, line)
	}
	if *Snapshot(track).Progress != 23.4 {
		t.Fatal("invalid progress replaced a valid reading")
	}
	updateProgress(track.Id, "GROOVIO_PROCESSING")
	job = Snapshot(track)
	if *job.Progress != 100 || job.Stage != "processing" {
		t.Fatal("audio preparation not reported")
	}
	track.Set("download_status", "failed")
	if Snapshot(track).Progress != nil {
		t.Fatal("failed job leaked old progress")
	}
	track.Set("download_status", "queued")
	if Snapshot(track).Stage != "" {
		t.Fatal("retried job leaked old stage")
	}
}

func TestDeezerAudioSearchPreservesRecordingVersion(t *testing.T) {
	app := testApp(t)
	track, err := QueueTrack(app, DownloadRequest{DeezerID: 3285732771, Title: "Creepy (Skunk)", Artist: "Omerta"})
	if err != nil {
		t.Fatal(err)
	}
	track.Set("metadata_source", "deezer")
	args := strings.Join(createYTDLPCommand(context.Background(), track, "output.mp3").Args, " ")
	if !strings.Contains(args, "Omerta Creepy (Skunk) official audio") {
		t.Fatal("lost authoritative recording version", args)
	}
	if !strings.Contains(args, "download:GROOVIO_PROGRESS:") {
		t.Fatal("missing structured progress output")
	}
}
