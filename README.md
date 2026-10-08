# Groovio Music Service

PocketBase backend for Groovio. Deezer supplies ranked song discovery and authoritative track metadata, MusicBrainz enriches verified matches, and yt-dlp and FFmpeg prepare tagged MP3 files for the app's offline library. Personal playlists and listening statistics stay in the app's local database.

## Run locally in WSL

```sh
bash scripts/dev.sh
```

Default API address: `http://127.0.0.1:8090`. Use `GET /api/health` to check it. PocketBase stores data and durable download jobs in the ignored `pb_data` folder.

Requires Go 1.24.6 or newer, Linux `yt-dlp`, `ffmpeg`, and `ffprobe`. The startup script includes the existing local FFmpeg installation if present.

The default User-Agent is `Groovio/0.1.0 (https://musicbrainz.org/user/FlyDoodle)`. Override `MUSICBRAINZ_USER_AGENT` in `.env.local` when needed. Public metadata requests require no Spotify credentials, MusicBrainz password, or OAuth.

For a physical phone, configure a reachable service address in Groovio's Search settings. Setting `MUSIC_SERVICE_HTTP=0.0.0.0:8090` binds the service on its network interfaces. Windows/WSL forwarding and firewall configuration depend on the local setup; no emulator forwarding script is assumed.

## API

| Method | Endpoint | Behavior |
| --- | --- | --- |
| GET | `/api/search?q=...&offset=0` | Ranked Deezer song search, 20 results per page |
| POST | `/api/queue-track` | Persist an idempotent job and return its ID/state with HTTP 202 |
| GET | `/api/downloads/{id}` | Job state, authoritative metadata, and failure details |
| GET | `/api/downloads/{id}/file` | Completed tagged MP3; supports byte ranges |

Queue payload:

```json
{
  "deezerId": 716493312,
  "title": "Optional provisional title",
  "artist": "Optional provisional artist"
}
```

The worker fetches the Deezer track before preparing audio; provisional names never override authoritative metadata. MusicBrainz enrichment first tries the ISRC, then a title/artist search whose recording identity and duration must match. A missing or unavailable enrichment result keeps the Deezer metadata. Album-specific Deezer IDs remain distinct even when they share a MusicBrainz recording.

Legacy payloads containing a MusicBrainz `recordingId` and optional `releaseId` remain supported. The worker verifies that the selected release contains the recording. A positive `deezerId` takes precedence and ignores caller-provided MusicBrainz references.

Jobs move through `queued → resolving → downloading → completed`, or `failed`. Repeated Deezer track requests reuse their existing job; legacy requests deduplicate by recording/release. Requeueing a failed job clears its error and retries it.

Prepared audio permits files up to 100 MiB, so normal high-quality MP3 songs are not rejected by PocketBase's default 5 MiB limit. A rejected upload preserves any existing file and still allows the worker to persist a retryable failure state.

## Queue and catalog traffic

Queue acknowledgment never waits for a catalog request. Workers resolve metadata in the background, with at most two active audio jobs. Work starts on enqueue/completion and is also checked by a periodic cron. Interrupted resolving/downloading jobs return to queued when the service restarts.

Deezer search and track lookups share a bounded five-minute cache and serialized request gate. MusicBrainz enrichment uses one application-wide client that spaces requests by at least 1.1 seconds. It caches successful responses for ten minutes, bounds the cache, retries 429/503 responses, and honors Retry-After. Run one service instance: worker claims, caching, and request limiting are process-local.

The hosted service must remain running to process its queue. Hosts that automatically suspend idle machines, including the existing Fly configuration, may pause workers until the next request restarts the service; the persisted queue is recovered on startup.

Migrations add MusicBrainz recording/release IDs, ISRC, Deezer track IDs, metadata source, job errors, and resolving status. Partial unique indexes enforce Deezer identity separately from legacy MusicBrainz identity. Existing Spotify records and their legacy playback/check endpoints are retained; new jobs no longer fetch Spotify metadata.

MP3 tags include title, artist, album, album artist, year, available cover art, Deezer track ID, available MusicBrainz recording/release/artist IDs, ISRC, `GROOVIO_METADATA_SOURCE`, and `GROOVIO_ORIGIN=service`. ISRC is also retained when MusicBrainz enrichment finds no match. Search qualifiers such as remix/live versions are retained in audio queries. Missing catalog durations do not exclude every audio candidate. Audio matching still depends on yt-dlp's search results.

## Temporary playback and audio retention

Play prepares audio on the server and uses the ranged file endpoint directly. It does not add a song or audio bytes to the app's offline library; Download remains the explicit offline-save action.

Completed server audio expires after 24 hours without a prepare/file request. Cleanup runs at startup and every 15 minutes, preserving catalog metadata and marking the job expired. A later request requeues the same job and regenerates its audio. Local offline copies are unaffected.

The server also targets a 1 GiB audio cache, evicting the least recently used files under storage pressure. Active file responses and files accessed within the last hour are protected, so this target can temporarily be exceeded. File access refreshes retention at both the beginning and end of the response. Legacy playback uses the same access protection.

## Verification

```sh
go test ./...
go vet ./...
GROOVIO_LIVE_TEST=1 go test ./catalog -run TestMusicBrainzLive -v
```

Tests cover Deezer ranking/pagination/cache, MusicBrainz enrichment identity and fallback, shared throttling/caching, Retry-After, cancellation, invalid recording/release combinations, duplicate queue submissions, distinct Deezer editions sharing MusicBrainz references, retries, persisted worker output, ID3 provenance, and full/suffix byte ranges. The worker test uses a local fake yt-dlp executable, and the opt-in live test only reads public MusicBrainz metadata.
