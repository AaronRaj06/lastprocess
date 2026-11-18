#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/azure-common.sh"
az vm start -g "$RG" -n "$VM"
ip=$(az vm list-ip-addresses -g "$RG" -n "$VM" --query '[0].virtualMachine.network.publicIpAddresses[0].ipAddress' -o tsv)
echo "VM running at $ip"
echo "If this differs from the DuckDNS record for lastprocess-arena.duckdns.org, update it there."
