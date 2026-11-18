# Azure budget

Subscription: **Azure for Students**. Starting credit **$100 on 2026-11-18**, expiring **2027-11-18**. No payment method is attached, so the subscription cannot bill a card. When the credit runs out, resources stop.

Rules: never add a payment method, upgrade, or switch to pay-as-you-go. Only the resources below may exist.

## What exists (created 2026-11-18 with the Azure CLI)

The subscription's policy allows only five regions (`indonesiacentral`, `uaenorth`, `malaysiawest`, `eastasia`, `polandcentral`). `Standard_B1s` is `NotAvailableForSubscription` in all five. The only region that offers a free-services size is **East Asia (Hong Kong)**, where `Standard_B2pts_v2` and `Standard_B2ats_v2` are both unrestricted. Checked with `az rest` on `Microsoft.Compute/skus` and `az policy assignment list`.

| Resource | Name | Details |
|---|---|---|
| Resource group | `lastprocess-rg` | eastasia |
| VM | `lastprocess-vm` | `Standard_B2pts_v2`: 2 vCPU (Neoverse-N1, Arm64), 1 GiB RAM; Ubuntu 24.04 LTS arm64; security type Standard; no boot diagnostics |
| OS disk | `lastprocess-vm_OsDisk_1_…` | 30 GB **Standard SSD** (`StandardSSD_LRS`, E4), not the default Premium SSD |
| Public IP | `lastprocess-vmPublicIP` | Standard SKU, static, `20.205.110.50` |
| Network security group | `lastprocess-vmNSG` | allow TCP 22 (priority 1000), TCP 2200 (priority 1010) |
| Virtual network, NIC | `lastprocess-vmVNET`, `lastprocess-vmVMNic` | required by every VM; no charge |
| Network Watcher | `NetworkWatcher_eastasia` in `NetworkWatcherRG` | **created automatically by Azure** with the VNet; believed free when no flow logs or connection monitors are enabled (none are). Not deleted. |

Commands that created them:
```
az group create -n lastprocess-rg -l eastasia
az vm create -g lastprocess-rg -n lastprocess-vm \
  --image Canonical:ubuntu-24_04-lts:server-arm64:latest --size Standard_B2pts_v2 \
  --admin-username azureuser --ssh-key-values ~/.ssh/lastprocess_key.pub \
  --storage-sku StandardSSD_LRS --public-ip-sku Standard --nsg-rule SSH
az vm open-port -g lastprocess-rg -n lastprocess-vm --port 2200 --priority 1010
```

## Cost estimate

Prices: Azure retail prices API (`prices.azure.com`), East Asia, list price in USD, read 2026-11-18. A month is 730 hours.

| Item | List price | Per month, VM running | Per month, VM deallocated |
|---|---|---|---|
| `B2pts_v2` Linux compute | $0.0116 / hour | $8.47, **or $0 if the student free hours apply** (750 h/month for this size, for the first 12 months, per the offer page; not verifiable from the CLI) | $0 |
| Standard static public IPv4 | $0.005 / hour | $3.65 | $3.65 (charged while allocated, even with the VM stopped) |
| E4 Standard SSD, 30 GB | $2.40 / month + $0.403 / month mount fee | $2.80 | $2.80 |
| Standard SSD transactions | $0.002 per 10,000 | about $0.05 (assumed under 250k operations a month) | ~$0 |
| Outbound data | first 100 GB/month free (Azure, as of our knowledge) | ~$0 (a session uses about 1.2 kB/s) | $0 |
| **Total** | | **about $6.50 with free compute; about $15 without** | **about $6.45** |

Projection: at $6.50 a month, $100 lasts about 15 months, past the 2027-11-18 expiry. At $15 a month it lasts about 6.6 months, until about June 2027, so **check after the first week whether compute is being charged**.

Deallocating saves only the compute line. Most of the monthly cost is the IP and the disk, which stay. Real savings would need deleting the IP (the address changes) or the whole VM, and that needs the owner's approval.

## How to check

`scripts/azure-cost-check.sh` (read-only) prints the subscription, the VM power state, every resource in the subscription, and usage from `az consumption usage list`. Tested 2026-11-18:
- `az consumption usage list` works but returned no rows on day one (usage data lags 8 to 24 hours).
- **The credit balance is not available from the CLI**: `Microsoft.Consumption/balances` returns Not Found for this offer. Read it in the portal (Education → Overview, or Cost Management) and log it below.

## Trigger rules

- Credit used exceeds 30% in the first month, or the monthly burn projects running out before 2027-11-18 → switch to **demo-window mode** and tell the owner.
- Credit remaining under $20 → demo-window mode regardless, and warn the owner.
- Any unexpected resource or line item in the cost check → stop, report it, wait for the owner's decision. Delete nothing.
- Subscription disabled or credit exhausted → run `scripts/backup.sh` if the VM is reachable, then follow `docs/FALLBACK.md`, update the README and the `ssh` address line, and report.

## Demo-window mode

Run the VM only during placement and interview windows:
- `scripts/azure-stop.sh`: backs up the data volume, then `az vm deallocate`. Compute stops. **Still charged while deallocated:** the static public IP ($3.65/month) and the OS disk ($2.80/month).
- `scripts/azure-start.sh`: `az vm start`, prints the public IP. The IP is static, so it should not change. If it does, update `lastprocess-arena.duckdns.org`.

## Log

| Date | Credit remaining | Note |
|---|---|---|
| 2026-11-18 | $100.00 (starting credit) | Resources created; not yet read from the portal |
