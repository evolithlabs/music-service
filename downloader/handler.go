package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bogem/id3v2"
	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// ======================================================================
// MAIN HANDLER ENTRY
// ======================================================================
func DownloadTrack(ctx context.Context, app core.App, track *core.Record) (*core.Record, error) {
	activeProgress.Store(track.Id, downloadProgress{Stage: "starting"})
	defer activeProgress.Delete(track.Id)
	// Download from youtube
	downloadDir := "./downloads"
	if err := os.MkdirAll(downloadDir, 0700); err != nil {
		return nil, err
	}

	fileID := uuid.New().String()
	tmpFile := filepath.Join(downloadDir, fmt.Sprintf("%s.mp3", fileID))

	defer os.Remove(tmpFile)
	if err := downloadAudio(ctx, track, tmpFile, func(cmd *exec.Cmd) error { return cmd.Run() }); err != nil {
		return nil, err
	}

	// Apply ID3 tags
	updateProgress(track.Id, "GROOVIO_PROCESSING")
	if err := writeID3Tags(track, tmpFile, fileID, downloadDir); err != nil {
		return nil, err
	}

	// Save the prepared file using PocketBase's configured storage.
	record, err := updateTrackRecord(app, track, tmpFile)
	if err != nil {
		return nil, fmt.Errorf("record save error: %w", err)
	}

	// delete local temp file
	os.Remove(tmpFile)

	return record, nil
}

// ======================================================================
//  YT-DLP COMMAND
// ======================================================================

func createYTDLPCommand(ctx context.Context, track *core.Record, tmpFile string) *exec.Cmd {
	return audioSearchCommand(ctx, track, tmpFile, true)
}

func downloadAudio(ctx context.Context, track *core.Record, tmpFile string, run func(*exec.Cmd) error) error {
	var lastErr error
	// The official-audio query can hide a matching upload. Try the exact recording
	// without that suffix before reporting it unavailable; keep its version intact.
	for _, official := range []bool{true, false} {
		if err := ctx.Err(); err != nil {
			return err
		}
		cmd := audioSearchCommand(ctx, track, tmpFile, official)
		cmd.Stdout = &progressOutput{jobID: track.Id, output: os.Stdout}
		cmd.Stderr = &progressOutput{jobID: track.Id, output: os.Stderr}
		err := run(cmd)
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 101 {
			err = nil
		}
		if err == nil {
			if info, statErr := os.Stat(tmpFile); statErr == nil && info.Size() > 0 {
				return nil
			}
		}
		lastErr = err
		os.Remove(tmpFile)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if lastErr != nil {
		return fmt.Errorf("YouTube could not provide audio for this recording. Try again or choose another version of the song.")
	}
	return fmt.Errorf("No matching YouTube audio was found for this recording. Try another version of the song.")
}

func audioSearchCommand(ctx context.Context, track *core.Record, tmpFile string, official bool) *exec.Cmd {
	title := track.GetString("name")
	if track.GetString("musicbrainz_recording_id") == "" && track.GetString("metadata_source") != "deezer" {
		title = cleanTrackName(title)
	}
	search := fmt.Sprintf("%s %s", track.GetString("artist"), title)
	if official {
		search += " official audio"
	}

	desired := track.GetInt("duration") / 1000
	// Music videos may have a short intro or outro. A balanced tolerance avoids
	// rejecting them while excluding full recordings for a short TV edit.
	tolerance := min(30, max(10, desired/10))
	minimum := max(0, desired-tolerance)
	maximum := desired + tolerance

	args := []string{
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "0",
		"--output", tmpFile,
		"--format", "bestaudio/best",
		"--no-playlist",
		"--max-downloads", "1",
		"--newline", "--no-color", "--progress",
		"--progress-delta", "0.25",
		"--progress-template", "download:GROOVIO_PROGRESS:%(progress._percent_str)s",
		"--progress-template", "postprocess:GROOVIO_PROCESSING",
	}
	if desired > 0 {
		args = append(args, "--match-filter", fmt.Sprintf("duration>=%d & duration<=%d", minimum, maximum))
	}
	args = append(args, "--", fmt.Sprintf("ytsearch10:%s", search))
	return exec.CommandContext(ctx, "yt-dlp", args...)
}

func cleanTrackName(n string) string {
	if i := strings.Index(n, "("); i != -1 {
		n = n[:i]
	}
	if i := strings.Index(n, "["); i != -1 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

// ======================================================================
//  ID3 TAG WRITING
// ======================================================================

func writeID3Tags(track *core.Record, tmpFile, fileID, dir string) error {
	tag, err := id3v2.Open(tmpFile, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("id3 open error: %w", err)
	}
	defer tag.Close()

	tag.SetVersion(3)
	// ID3v2.3 defaults to Latin-1; catalog metadata requires Unicode.
	tag.SetDefaultEncoding(id3v2.EncodingUTF16)
	tag.SetTitle(track.GetString("name"))

	tag.SetArtist(track.GetString("artist"))
	tag.SetAlbum(track.GetString("album"))
	tag.AddTextFrame("TPE2", tag.DefaultEncoding(), track.GetString("album_artist"))

	fullDate := track.GetString("release_date") // "2021-08-23"
	year := ""
	if len(fullDate) >= 4 {
		year = fullDate[:4] // "2021"
	}
	tag.SetYear(year)

	// Album art
	if len(track.GetString("cover_url")) > 0 {
		coverURL := track.GetString("cover_url")
		coverPath := filepath.Join(dir, fmt.Sprintf("%s_cover.jpg", fileID))
		if err := downloadFile(coverURL, coverPath); err == nil {
			imgBytes, _ := os.ReadFile(coverPath)
			tag.AddAttachedPicture(id3v2.PictureFrame{
				Encoding:    tag.DefaultEncoding(),
				MimeType:    http.DetectContentType(imgBytes),
				PictureType: id3v2.PTFrontCover,
				Picture:     imgBytes,
			})
			os.Remove(coverPath)
		}
	}

	// Preserve metadata provenance independently from the audio source.
	values := map[string]string{
		"GROOVIO_ORIGIN":          "service",
		"GROOVIO_METADATA_SOURCE": track.GetString("metadata_source"),
		"SPOTIFY_ID":              track.GetString("spotify_track_id"),
	}
	if id := track.GetInt("deezer_id"); id > 0 {
		values["DEEZER_TRACK_ID"] = fmt.Sprint(id)
	}
	if recordingID := track.GetString("musicbrainz_recording_id"); recordingID != "" {
		values["MUSICBRAINZ_TRACKID"] = recordingID
		values["MUSICBRAINZ_ALBUMID"] = track.GetString("album_id")
		values["MUSICBRAINZ_ARTISTID"] = track.GetString("artist_id")
		values["MUSICBRAINZ_ALBUMARTISTID"] = track.GetString("album_artist_id")
	}
	if isrc := track.GetString("isrc"); isrc != "" {
		tag.AddTextFrame("TSRC", tag.DefaultEncoding(), isrc)
	}
	for key, value := range values {
		if value != "" {
			tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{
				Encoding: tag.DefaultEncoding(), Description: key, Value: value,
			})
		}
	}
	return tag.Save()
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artwork HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, io.LimitReader(resp.Body, 10<<20))
	return err
}

// ======================================================================
//  SAVE RECORD TO POCKETBASE
// ======================================================================

func updateTrackRecord(app core.App, track *core.Record, localPath string) (*core.Record, error) {
	file, err := filesystem.NewFileFromPath(localPath)
	if err != nil {
		return nil, err
	}

	originalFile := track.Get("file")
	track.Set("file", file)
	if err := app.Save(track); err != nil {
		// Do not leave a rejected upload attached when the worker saves its failure state.
		track.Set("file", originalFile)
		return nil, err
	}

	return track, nil
}
