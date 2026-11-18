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
ssh -i "$key" -p "$port" "$user@$host" \
  'docker run --rm -v lastprocess_lastprocess-data:/d:ro alpine tar czf - -C /d .' > "$out"
chmod 600 "$out"
tar tzf "$out" >/dev/null   # fails if the archive is truncated
echo "Backup written: $out ($(wc -c < "$out") bytes)"
