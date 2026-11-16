#!/usr/bin/env bash
# run.sh: start the karaoke server on this computer, building it first if you like.
#
#   ./run.sh           asks: build and run, or just run (it suggests building when the code
#                      has changed since ./karaoke was built)
#   ./run.sh build     builds, then runs
#   ./run.sh run       runs ./karaoke as it is
#   ./run.sh local     answers on this computer only; goes with the others: ./run.sh build local
#
# Otherwise the server answers on every address this computer has: localhost, the LAN and
# Tailscale alike. If ufw would keep phones out, it prints the commands that let them in.
# Anything else goes to the server:  ./run.sh run -library ~/Karaoke
# LISTEN sets the address outright (default :8765); OPEN=0 leaves the TV page closed.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"

LISTEN=${LISTEN:-:8765}
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

# The firewall check. Connections from this computer skip the firewall, so the server can't
# notice it is blocked: phones just never connect. ufw's rules are read from its own files,
# since `ufw status` needs root.
UFW=/etc/ufw
UFW_DEFAULTS=/etc/default/ufw

# ip4 turns a dotted IPv4 address into a number.
ip4() {
  local IFS=. a b c d
  read -r a b c d <<<"$1"
  echo $(((a << 24) | (b << 16) | (c << 8) | d))
}

# network turns an address and its prefix (10.0.0.4/24) into its network (10.0.0.0/24).
network() {
  local bits=${1#*/} n
  n=$(($(ip4 "${1%/*}") & (0xffffffff << (32 - bits)) & 0xffffffff))
  echo "$((n >> 24 & 255)).$((n >> 16 & 255)).$((n >> 8 & 255)).$((n & 255))/$bits"
}

# net_has says whether network $1 (a.b.c.d/n or a bare address) holds all of network $2.
net_has() {
  local outer=${1%/*} obits=${1#*/} inner=${2%/*} ibits=${2#*/} mask
  [ "$obits" != "$1" ] || obits=32
  [ "$ibits" != "$2" ] || ibits=32
  mask=$((obits ? (0xffffffff << (32 - obits)) & 0xffffffff : 0))
  [ "$obits" -le "$ibits" ] && [ $(($(ip4 "$outer") & mask)) -eq $(($(ip4 "$inner") & mask)) ]
}

# port_in says whether a ufw port field $2 (any, 8765, 8000:9000, 80,443) takes in port $1.
port_in() {
  local part IFS=,
  [ "$2" != any ] || return 0
  for part in $2; do
    case $part in
      *:*) [ "$1" -ge "${part%:*}" ] && [ "$1" -le "${part#*:}" ] && return 0 ;;
      *) [ "$1" = "$part" ] && return 0 ;;
    esac
  done
  return 1
}

# ufw_open prints "interface source destination" for each ufw rule that lets TCP port $1 in;
# the interface is - when the rule holds for all of them.
ufw_open() {
  local h1 h2 h3 action proto dport dst sport src rest dir
  while read -r h1 h2 h3 action proto dport dst sport src rest; do
    [ "$h1 $h2 $h3" = '### tuple ###' ] || continue
    rest=${rest%% comment=*}
    dir=${rest##* } # app rules put two app names before it
    case $action in allow | limit) ;; *) continue ;; esac
    case $proto in tcp | any) ;; *) continue ;; esac
    [ "$sport" = any ] || continue
    port_in "$1" "$dport" || continue
    case $dir in
      in) echo "- $src $dst" ;;
      in_*) echo "${dir#in_} $src $dst" ;;
    esac
  done <"$UFW/user.rules"
}

# firewall_hint prints the ufw commands that let phones in, when ufw would keep phones on the
# LAN or Tailscale away from the port.
firewall_hint() {
  local host=${LISTEN%:*} readable='' rules=() cmds=() where=() iface cidr src cmd label rule ri rs rd open list
  local -A seen=()
  host=${host#[}
  host=${host%]}
  case $host in
    127.* | localhost | ::1) return 0 ;;
    0.0.0.0 | ::) host= ;;
  esac
  grep -qx 'ENABLED=yes' "$UFW/ufw.conf" 2>/dev/null || return 0
  grep -q '^DEFAULT_INPUT_POLICY="ACCEPT"' "$UFW_DEFAULTS" 2>/dev/null && return 0
  command -v ip >/dev/null || return 0
  if [ -r "$UFW/user.rules" ]; then
    readable=1
    mapfile -t rules < <(ufw_open "$PORT")
  fi
  while read -r _ iface _ cidr _; do
    [ -z "$host" ] || [ "${cidr%/*}" = "$host" ] || continue
    case $iface in
      docker* | br-* | veth* | virbr* | podman* | cni*) continue ;;
      tailscale*)
        src=100.64.0.0/10 # every tailnet address
        cmd="sudo ufw allow in on $iface to any port $PORT proto tcp comment 'karaoke tailnet'"
        label=Tailscale ;;
      *)
        src=$(network "$cidr")
        cmd="sudo ufw allow from $src to any port $PORT proto tcp comment 'karaoke LAN'"
        label="the LAN ($src)" ;;
    esac
    open=
    for rule in "${rules[@]}"; do
      read -r ri rs rd <<<"$rule"
      [ "$ri" = - ] || [ "$ri" = "$iface" ] || continue
      if net_has "$rs" "$src" && net_has "$rd" "${cidr%/*}"; then open=1; break; fi
    done
    [ -z "$open" ] && [ -z "${seen[$cmd]-}" ] || continue
    seen[$cmd]=1
    cmds+=("$cmd")
    where+=("$label")
  done < <(ip -4 -o addr show scope global)
  [ ${#cmds[@]} -gt 0 ] || return 0
  list=$(printf ', %s' "${where[@]}")
  list=${list:2}
  [[ $list != *", "* ]] || list="${list%, *} and ${list##*, }"
  if [ -n "$readable" ]; then
    echo "ufw keeps phones on $list out of port $PORT. To let them in:"
  else
    echo "ufw is on and only root can read its rules: if phones on $list can't join, run"
  fi
  printf '  %s\n' "${cmds[@]}"
}

mode=
while [ $# -gt 0 ]; do
  case $1 in
    build | run) mode=$1 ;;
    local) LISTEN=127.0.0.1:$PORT ;;
    *) break ;;
  esac
  shift
done
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
firewall_hint
echo "Starting: ./karaoke ${flags[*]} $*"
exec ./karaoke "${flags[@]}" "$@"
