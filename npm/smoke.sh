#!/usr/bin/env bash
# Dry run of the npm release (CI "npm-smoke", docs/LOCAL.md): build this
# machine's binary, pack every npm package, install the tarballs into an empty
# project and run what a user runs:
#
#   jungle-monkey --version
#   jungle-monkey start --no-open        (FLEETPOCKET_RUNNER_FAKE=true)
#   jungle-monkey add <temp git repo>
#   POST /api/v1/tasks, answer the question, wait for branch_ready
#   the jm/... branch exists in the temp repo
#   jungle-monkey stop
#
# SMOKE_DOCKER=0 stops after --version and doctor (CI macOS runners have no
# Docker). RUNNER_IMAGE defaults to fleetpocket-agent:dev (`docker build -t
# fleetpocket-agent:dev agent`). Needs go, node/npm, git, curl and jq.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/jm-smoke.XXXXXX")
version=0.0.0-smoke
export JM_DATA_DIR="$work/data"
export RUNNER_IMAGE=${RUNNER_IMAGE:-fleetpocket-agent:dev}
export FLEETPOCKET_RUNNER_FAKE=true
log() { printf '\n== %s\n' "$*"; }

cleanup() {
  if [ -n "${jm:-}" ] && [ -x "$jm" ]; then "$jm" stop >/dev/null 2>&1 || true; fi
  if [ -n "${KEEP_SMOKE:-}" ]; then echo "kept $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT

# 1. This machine's binary (with the web app embedded); stubs for the other
#    targets so every package can be packed.
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) key=linux-x64 goos=linux goarch=amd64 ;;
  Linux-aarch64 | Linux-arm64) key=linux-arm64 goos=linux goarch=arm64 ;;
  Darwin-arm64) key=darwin-arm64 goos=darwin goarch=arm64 ;;
  Darwin-x86_64) key=darwin-x64 goos=darwin goarch=amd64 ;;
  *) echo "unsupported host $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
log "build $key"
bins="$work/bin"
mkdir -p "$bins"
if [ ! -f "$root/server/internal/webui/dist/index.html" ]; then
  (cd "$root" && make embed-web)
fi
(cd "$root/server" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -tags embedweb -trimpath \
  -ldflags "-s -w -X github.com/andrewsl105/fleetpocket/server/internal/config.Version=v$version" \
  -o "$bins/jungle-monkey-$key" ./cmd/jungle-monkey)
for k in darwin-arm64 darwin-x64 linux-x64 linux-arm64 win32-x64; do
  ext=; [ "$k" = win32-x64 ] && ext=.exe
  [ -f "$bins/jungle-monkey-$k$ext" ] || echo "stub" >"$bins/jungle-monkey-$k$ext"
done

# 2. Pack every package.
log "npm pack"
node "$root/npm/build.mjs" "$version" "$bins"
tarballs="$work/tgz"
mkdir -p "$tarballs"
for d in "$root"/npm/dist/*/; do
  (cd "$d" && npm pack --silent --pack-destination "$tarballs" >/dev/null)
done
ls "$tarballs"

# 3. Install the tarballs into an empty project: the main package and this
#    machine's platform package (the others are optional and absent).
log "npm install"
app="$work/app"
mkdir -p "$app"
(cd "$app" && npm init -y >/dev/null && npm install --no-audit --no-fund --loglevel=error \
  "$tarballs/jungle-monkey-$version.tgz" "$tarballs/jungle-monkey-$key-$version.tgz")
jm="$app/node_modules/.bin/jungle-monkey"

log "jungle-monkey --version"
out=$("$jm" --version)
echo "$out"
[ "$out" = "jungle-monkey v$version" ] || { echo "unexpected version: $out" >&2; exit 1; }

if [ "$(uname -s)" = Darwin ]; then
  # npm does not set the quarantine attribute, so Gatekeeper never asks.
  bin="$app/node_modules/@jungle-monkey/$key/bin/jungle-monkey"
  if xattr "$bin" 2>/dev/null | grep -q com.apple.quarantine; then
    echo "the npm-installed binary is quarantined" >&2; exit 1
  fi
fi

if [ "${SMOKE_DOCKER:-1}" = 0 ]; then
  log "doctor (no Docker on this runner)"
  "$jm" doctor || true
  echo "smoke (no Docker) passed"
  exit 0
fi

# 4. Start, add a repository, run a task to branch_ready, stop.
log "jungle-monkey start --no-open"
"$jm" start --no-open >"$work/start.log" 2>&1 &
for _ in $(seq 1 180); do
  if "$jm" status >/dev/null 2>&1; then break; fi
  sleep 1
done
"$jm" status || { cat "$work/start.log"; exit 1; }

repo="$work/repo"
git init -q -b main "$repo"
git -C "$repo" -c user.name=smoke -c user.email=smoke@example.com commit -q --allow-empty -m init
log "jungle-monkey add"
(cd "$work" && "$jm" add "$repo")

cfg="$JM_DATA_DIR/config.json"
port=$(jq -r .port "$cfg")
token=$(jq -r .control_token "$cfg")
base="http://127.0.0.1:$port"
jar="$work/cookies"
api() { curl -fsS -b "$jar" -H 'X-Requested-With: fleetpocket' -H 'Content-Type: application/json' "$@"; }

# A browser session from a one-time login code.
code=$(curl -fsS -X POST -H "Authorization: Bearer $token" -H 'X-Requested-With: fleetpocket' "$base/api/v1/local/login-code" | jq -r .code)
curl -fsS -o /dev/null -c "$jar" "$base/auth/local?code=$code"

project=$(api "$base/api/v1/projects" | jq -r --arg p "$(cd "$repo" && pwd -P)" \
  '.items[] | select(.repositories[]?.path == $p) | .id' | head -1)
[ -n "$project" ] || { echo "the added repository has no project" >&2; exit 1; }
log "task in project $project"
task=$(api -X POST "$base/api/v1/tasks" -d "{\"project_id\":\"$project\",\"prompt\":\"Say hello\"}" | jq -r .id)

status=
for _ in $(seq 1 240); do
  view=$(api "$base/api/v1/tasks/$task")
  status=$(echo "$view" | jq -r .status)
  case "$status" in
    waiting_input)
      q=$(echo "$view" | jq -r .open_question.id)
      api -X POST "$base/api/v1/tasks/$task/answer" -d "{\"question_id\":\"$q\",\"text\":\"hello\"}" >/dev/null || true
      ;;
    branch_ready) break ;;
    failed | cancelled | completed)
      echo "task ended $status: $(echo "$view" | jq -r .status_reason)" >&2; exit 1 ;;
  esac
  sleep 1
done
[ "$status" = branch_ready ] || { echo "task is $status, not branch_ready" >&2; exit 1; }
branch=$(api "$base/api/v1/tasks/$task" | jq -r .local_branch)
log "delivered $branch"
git -C "$repo" rev-parse --verify --quiet "refs/heads/$branch" >/dev/null || { echo "$branch not in the repository" >&2; exit 1; }
case "$branch" in jm/*) ;; *) echo "unexpected branch name $branch" >&2; exit 1 ;; esac
[ "$(git -C "$repo" symbolic-ref --short HEAD)" = main ] || { echo "delivery changed the checked-out branch" >&2; exit 1; }

log "jungle-monkey stop"
"$jm" stop
echo "smoke passed"
