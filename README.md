# Groovio Music Service

PocketBase backend for the Groovio mobile music player.

## Run locally in WSL

```bash
bash scripts/dev.sh
```

The API listens on `http://127.0.0.1:8090`. Check `/api/health` for status.
PocketBase stores local data in the ignored `pb_data` directory.

Requires Go 1.24.6 or newer. Download processing also requires Linux `yt-dlp`,
`ffmpeg` and `ffprobe` on PATH. The startup script includes the local FFmpeg
installation at `~/.local/share/music-service/ffmpeg/bin` if present.

For Spotify metadata and download jobs, copy `.env.example` to `.env.local`,
fill in your Spotify application credentials, then restart the service.
Do not commit credentials. Local music playback in the phone app does not
require Spotify credentials.

The Android development script forwards the emulator's port 8090 to the
Windows host, which forwards localhost to this WSL service.
