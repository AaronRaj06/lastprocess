#!/usr/bin/env bash
# Deallocate the VM: compute billing stops. The public IP and the OS disk still cost credit.
set -eu
. "$(dirname "$0")/azure-common.sh"
"$(dirname "$0")/backup.sh"
az vm deallocate -g "$RG" -n "$VM"
az vm get-instance-view -g "$RG" -n "$VM" --query 'instanceView.statuses[1].displayStatus' -o tsv
