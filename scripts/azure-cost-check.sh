#!/usr/bin/env bash
# Read-only: subscription, VM power state, every resource in the subscription, recent usage.
set -u
. "$(dirname "$0")/azure-common.sh"
echo "Subscription: $sub ($(az account show --query state -o tsv))"
echo "VM power state: $(az vm get-instance-view -g "$RG" -n "$VM" --query 'instanceView.statuses[1].displayStatus' -o tsv 2>/dev/null || echo 'not found')"
echo "Public IP: $(az vm list-ip-addresses -g "$RG" -n "$VM" --query '[0].virtualMachine.network.publicIpAddresses[0].ipAddress' -o tsv 2>/dev/null)"
echo
echo "All resources in the subscription (anything beyond the VM, its disk, NIC, IP, NSG and VNet is unexpected):"
az resource list --query '[].{type:type,name:name,group:resourceGroup}' -o table
echo
start=$(date -u -v-30d +%Y-%m-%d 2>/dev/null || date -u -d '30 days ago' +%Y-%m-%d)
echo "Usage and cost since $start (az consumption usage list):"
if ! az consumption usage list --start-date "$start" --end-date "$(date -u +%Y-%m-%d)" \
    --query '[].{date:usageStart,meter:meterDetails.meterName,qty:usageQuantity,cost:pretaxCost}' -o table 2>&1; then
  echo "(usage API not available for this subscription)"
fi
echo
echo "Credit balance: Azure for Students does not expose it through the CLI (as far as tested)."
echo "Read it in the portal (Education -> Overview, or Cost Management) and log it in docs/azure-budget.md."
