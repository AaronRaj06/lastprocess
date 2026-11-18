#!/usr/bin/env bash
# Copy the lastprocess-data volume (SQLite db + SSH host key) from the VM to backups/ here.
set -eu
host=${DEPLOY_HOST:-lastprocess-arena.duckdns.org}
user=${DEPLOY_USER:-azureuser}
port=${DEPLOY_PORT:-2200}
key=${KEY:-$HOME/.ssh/lastprocess_key}
dir="$(cd "$(dirname "$0")/.." && pwd)/backups"
mkdir -p "$dir" && chmod 700 "$dir"
out="$dir/lastprocess-data-$(date +%Y%m%d-%H%M%S).tgz"
tmp="$out.partial"
trap 'rm -f "$tmp"' EXIT   # never leave a truncated archive behind
umask 077
# The game is stopped for the few seconds of the copy, so the SQLite file and its WAL are consistent.
ssh -i "$key" -p "$port" "$user@$host" \
  'cd ~/lastprocess && docker compose stop -t 10 game >&2 &&
   docker run --rm -v lastprocess_lastprocess-data:/d:ro alpine tar czf - -C /d .; s=$?;
   docker compose start game >&2; exit $s' > "$tmp"
tar tzf "$tmp" | grep -q host_ed25519   # fails if truncated or empty
mv "$tmp" "$out"
echo "Backup written: $out ($(wc -c < "$out") bytes)"
