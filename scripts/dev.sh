#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# Optional local credentials; this file is ignored by Git.
if [[ -f .env.local ]]; then
  set -a
  source .env.local
  set +a
fi

export PATH="$HOME/.local/share/music-service/ffmpeg/bin:$HOME/.local/bin:$PATH"
exec go run . serve --http="${MUSIC_SERVICE_HTTP:-127.0.0.1:8090}"
