# Deploying

Target: any small Linux VM with Docker (amd64 or arm64). The live demo runs on an Azure for Students VM (see `docs/decisions.md` D16 and `docs/azure-budget.md`); moving to another host changes only the GitHub secrets, never the code or the workflow. If no host is available, see `docs/FALLBACK.md`.

The image is built in CI for `linux/amd64` and `linux/arm64` and pulled by the VM. Nothing is built on the VM.

## One-time VM setup (any Linux VM)

Keep your first SSH session open until step 3 is confirmed.

1. In the cloud firewall, open TCP **22** (the game) and TCP **2200** (admin SSH).
2. Move the VM's own sshd to port 2200 so the game can have port 22:
   ```
   sudo apt-get update && sudo apt-get install -y iptables-persistent
   sudo iptables -I INPUT -p tcp --dport 2200 -j ACCEPT && sudo netfilter-persistent save
   sudo sed -i 's/^#\?Port .*/Port 2200/' /etc/ssh/sshd_config
   # Ubuntu 24.04 starts sshd from ssh.socket, which ignores Port; switch to the plain service.
   sudo systemctl disable --now ssh.socket && sudo systemctl enable ssh && sudo systemctl restart ssh
   ```
3. From a second terminal: `ssh -p 2200 <user>@<host>`. Only close the first session once this works.
4. Install Docker: `curl -fsSL https://get.docker.com | sudo sh && sudo usermod -aG docker $USER` (log in again afterwards).
5. On a 1 GiB VM, add swap:
   ```
   sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
   echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
   ```
6. Copy the compose file: `scp -P 2200 deploy/docker-compose.yml <user>@<host>:~/lastprocess/`.
7. Point the DNS name (`lastprocess-arena.duckdns.org`) at the VM's public IP.

## Azure (the current host)

Provisioned with the Azure CLI. The exact commands, sizes and costs are logged in `docs/azure-budget.md`. In short: one resource group `lastprocess-rg`, one Ubuntu 24.04 VM (admin user `azureuser`, key `~/.ssh/lastprocess_key`) with a 30 GB Standard SSD OS disk, its public IP and network security group, and ports 22 and 2200 opened with `az vm open-port`. Nothing else.

If you lock yourself out of SSH, use the Azure run-command channel instead of recreating the VM:
```
az vm run-command invoke -g lastprocess-rg -n lastprocess-vm --command-id RunShellScript --scripts "systemctl restart ssh"
```

Stop and start the VM with `scripts/azure-stop.sh` and `scripts/azure-start.sh`; check spend with `scripts/azure-cost-check.sh`.

## Continuous deploy

`.github/workflows/deploy.yml` runs on every `v*` tag: it tests, builds the multi-arch image, pushes it to GHCR and restarts the service over SSH. Repository secrets:

| Secret | Value |
|---|---|
| `DEPLOY_HOST` | VM public IP or DNS name |
| `DEPLOY_USER` | VM login user (`azureuser` on Azure) |
| `DEPLOY_PORT` | admin SSH port (`2200`) |
| `DEPLOY_SSH_KEY` | private half of a deploy-only key authorized on the VM |

Make the GHCR package public once (GitHub → Packages → lastprocess → visibility) so the VM can pull without credentials.

Release: `git tag v0.1.0 && git push --tags`.

## What persists, and backups

The `lastprocess-data` volume holds `lastprocess.db` (accounts, bots, last 500 matches) and `host_ed25519` (the server's SSH identity). Run `scripts/backup.sh` before stopping, resizing or moving the VM; it copies the volume to `backups/` on your machine (git-ignored, never committed).

## Limits in production

Defaults: 256 sessions, 4 per client address, 1 new session per second per address (burst 10), 32 concurrent matches, 10 minute idle timeout. Change them with flags in the compose file (`command:`).
