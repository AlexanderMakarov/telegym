#!/usr/bin/env bash
# Re-record the README / site demos (run via `make demo`):
#   docs/demo.gif        terminal: Docker mock + echobot + k6 (charmbracelet/vhs)
#   docs/debug-chat.gif  browser: the debug chat (Playwright driving local Chrome)
#
# Needs vhs, Docker, ffmpeg, Node and Google Chrome; ports 5678 and 8443 free.
# Playwright is installed into a cache dir, not into the repo.
set -euo pipefail
cd "$(dirname "$0")/../.."

pw_dir="${XDG_CACHE_HOME:-$HOME/.cache}/telegym-demo"

# Check every prerequisite first and list all that are missing with a hint.
missing=()
need() { command -v "$1" >/dev/null || missing+=("$1: $2"); }
need vhs    "brew install vhs            (charmbracelet/vhs; pulls ttyd + ffmpeg)"
need ffmpeg "brew install ffmpeg         (apt install ffmpeg)"
need docker "https://docs.docker.com/get-docker/"
need node   "brew install node           (https://nodejs.org)"
need npm    "comes with node"
need curl   "brew install curl           (apt install curl)"
chrome_found=false
for c in "/Applications/Google Chrome.app" google-chrome google-chrome-stable; do
  if [ -e "$c" ] || command -v "$c" >/dev/null; then chrome_found=true; break; fi
done
$chrome_found || missing+=("Google Chrome: https://www.google.com/chrome/  (Playwright drives the installed Chrome)")
if command -v docker >/dev/null && ! docker info >/dev/null 2>&1; then
  missing+=("Docker daemon: start Docker Desktop (or the docker service)")
fi
if [ ${#missing[@]} -gt 0 ]; then
  echo "make demo: install these first:" >&2
  printf '  - %s\n' "${missing[@]}" >&2
  exit 1
fi
for port in 5678 8443; do
  if curl -s -o /dev/null "http://localhost:$port"; then
    echo "make demo: port $port is busy, stop whatever listens there" >&2
    exit 1
  fi
done

echo "==> docs/demo.gif"
vhs scripts/demo/demo.tape

echo "==> docs/debug-chat.gif"
if [ ! -d "$pw_dir/node_modules/playwright" ]; then
  npm install --silent --no-audit --no-fund --prefix "$pw_dir" playwright
  "$pw_dir/node_modules/.bin/playwright" install ffmpeg
fi

tmp=$(mktemp -d)
pids=()
cleanup() {
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$tmp"
}
trap cleanup EXIT

./bin/telegym-mock -quiet >/dev/null 2>&1 &
pids+=($!)
./bin/echobot -poll >/dev/null 2>&1 &
pids+=($!)
until curl -sf http://localhost:5678/health >/dev/null; do sleep 0.2; done
sleep 1

DEMO_TMP="$tmp" NODE_PATH="$pw_dir/node_modules" node scripts/demo/debug-chat.cjs
ffmpeg -v error -y -ss 0.5 -i "$tmp/debug-chat.webm" \
  -vf "fps=15,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle" \
  docs/debug-chat.gif

ls -lh docs/demo.gif docs/debug-chat.gif
