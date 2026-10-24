#!/usr/bin/env bash
# Records demo/demo.gif against a local test server (see CLAUDE.md, termost-mm).
# Run from the repo root: nix develop .#demo -c demo/record.sh
set -euo pipefail

url=${TERMOST_DEMO_URL:-http://localhost:8065}
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

go build -o bin/termost ./cmd/termost
go run ./demo/seed -url "$url"

mkdir -p "$scratch/config/termost" "$scratch/state" "$scratch/cache" "$scratch/run"
chmod 700 "$scratch/run"
cat >"$scratch/config/termost/config.toml" <<TOML
[[server]]
name = "acme"
url = "$url"
username = "maya"

[[server]]
name = "openkit"
url = "${url/localhost/127.0.0.1}"
username = "mchen"

[notifications]
desktop = false
TOML

export XDG_CONFIG_HOME=$scratch/config XDG_STATE_HOME=$scratch/state XDG_CACHE_HOME=$scratch/cache XDG_RUNTIME_DIR=$scratch/run
export DBUS_SESSION_BUS_ADDRESS=unix:path=$scratch/nobus

for server in acme openkit; do
  printf 'Passw0rd!x\n' | bin/termost login "$server" >/dev/null 2>&1
done

vhs demo/demo.tape
