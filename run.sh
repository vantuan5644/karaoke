#!/usr/bin/env bash
# run.sh: start the karaoke server on this computer, building it first if you like.
#
#   ./run.sh           asks: build and run, or just run (it suggests building when the code
#                      has changed since ./karaoke was built)
#   ./run.sh build     builds, then runs
#   ./run.sh run       runs ./karaoke as it is
#
# Anything else goes to the server:  ./run.sh run -library ~/Karaoke
# LISTEN is where it listens (default 127.0.0.1:8765); OPEN=0 leaves the TV page closed.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

LISTEN=${LISTEN:-127.0.0.1:8765}
OPEN=${OPEN:-1}
PORT=${LISTEN##*:}

# ask prints a question and reads one answer; without a terminal it takes the default.
ask() {
  local question=$1 default=$2 answer
  if [ -t 0 ]; then
    read -r -p "$question" answer || answer=
  else
    answer=
  fi
  answer=${answer:-$default}
  printf '%s' "${answer,,}"
}

# changed says whether the code is newer than ./karaoke (or there is no ./karaoke yet).
changed() {
  [ -x karaoke ] || return 0
  [ -n "$(find . -path ./.git -prune -o \( -name '*.go' -o -name go.mod -o -name go.sum -o -path './web/*' \) \
    -newer karaoke -print -quit)" ]
}

build() {
  echo "Building..."
  if command -v make >/dev/null; then make build; else go build -o karaoke .; fi
}

# stop_old offers to stop a karaoke server already on the port, since a second can't start.
stop_old() {
  local pid name
  pid=$(ss -ltnpH "sport = :$PORT" 2>/dev/null | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2 || true)
  [ -n "$pid" ] || return 0
  name=$(ps -o comm= -p "$pid" 2>/dev/null || true)
  if [ "$name" != karaoke ]; then
    echo "Port $PORT is taken by ${name:-another program} (pid $pid): set LISTEN to another address." >&2
    exit 1
  fi
  case $(ask "A karaoke server is already running on port $PORT (pid $pid). Stop it? [Y/n] " y) in
    y*) ;;
    *) echo "Left it running."; exit 0 ;;
  esac
  kill "$pid"
  for _ in $(seq 50); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.1
  done
  echo "pid $pid is still running after 5 s; stop it and try again." >&2
  exit 1
}

mode=
case ${1:-} in
  build | run) mode=$1; shift ;;
esac
if [ -z "$mode" ]; then
  suggest=r
  if changed; then
    suggest=b
    echo "The code has changed since ./karaoke was built."
  fi
  case $(ask "[b]uild and run, [r]un, or [q]uit? [$suggest] " "$suggest") in
    b*) mode=build ;;
    r*) mode=run ;;
    q*) exit 0 ;;
    *) echo "Answer b, r or q." >&2; exit 1 ;;
  esac
fi

if [ "$mode" = build ]; then
  build
elif [ ! -x karaoke ]; then
  echo "There is no ./karaoke yet: building it first."
  build
fi
stop_old
flags=(-listen "$LISTEN")
[ "$OPEN" = 1 ] && flags+=(-open)
echo "Starting: ./karaoke ${flags[*]} $*"
exec ./karaoke "${flags[@]}" "$@"
