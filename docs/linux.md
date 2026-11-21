# SHELFCODE on a Linux server

SHELFCODE runs as a normal systemd service. It runs as its own unprivileged user, keeps its data in `/var/lib/shelfcode`, and serves the web interface on port **8765**. Supported CPUs: x86_64 (amd64), aarch64 (arm64) and armv7 (Raspberry Pi 2/3 32-bit).

## Install (pick one)

### 1. One command (any systemd distro)

```bash
curl -fsSL https://github.com/ABelly99/Shelf-Code/releases/latest/download/install.sh | sudo bash
```

With options:

```bash
curl -fsSL https://github.com/ABelly99/Shelf-Code/releases/latest/download/install.sh | sudo bash -s -- --port 8765 --pin 1234 --open-firewall
```

| Option | Meaning |
|---|---|
| `--port N` | web port (default 8765) |
| `--pin PIN` | require a PIN on every device |
| `--open-firewall` | open the port in ufw / firewalld if one is active |
| `--version v1.0.0` | install a specific release |
| `--uninstall` | remove the program and service, keep data and settings |
| `--purge` | remove everything, including data, settings and the `shelfcode` user |

The installer:
- downloads the right build for your CPU and checks its SHA-256 checksum
- creates the `shelfcode` system user, `/var/lib/shelfcode` and `/etc/shelfcode/shelfcode.env`
- installs `/usr/local/bin/shelfcode` and the `shelfcode` service, then starts it and enables it at boot
- prints the address to open

Running it again **upgrades** SHELFCODE and keeps your data and settings.

### 2. Debian / Ubuntu package

Download `shelfcode_<version>_amd64.deb` (or `arm64` / `armhf`) from the [latest release](https://github.com/ABelly99/Shelf-Code/releases/latest), then:

```bash
sudo apt install ./shelfcode_*_amd64.deb
```

### 3. Fedora / RHEL / Rocky / Alma package

```bash
sudo dnf install --nogpgcheck ./shelfcode-*.x86_64.rpm
```

(`--nogpgcheck` is needed because the package is not GPG-signed. Newer Fedora refuses unsigned packages otherwise.)

The packages install `/usr/bin/shelfcode`, the service and `/etc/shelfcode/shelfcode.env` (kept on upgrade), create the user, and start the service. To upgrade, install the newer package the same way. To remove: `sudo apt remove shelfcode` or `sudo dnf remove shelfcode`. Your data in `/var/lib/shelfcode` is kept.

### 4. Manual (tarball)

```bash
tar -xzf shelfcode-linux-amd64.tar.gz
```

```bash
cd shelfcode-linux-amd64 && sudo ./install.sh
```

The script uses the program next to it instead of downloading. Or skip the installer and run the program directly: `./shelfcode --headless --data /srv/shelfcode`.

## Updates

With the installer (option 1 or 4), **every restart installs the newest release**:

```bash
sudo systemctl restart shelfcode
```

Before each start, the service runs `shelfcode self-update` as root. It checks GitHub, verifies the download's SHA-256, and replaces `/usr/local/bin/shelfcode` if there is a newer version. If the check fails (no internet, GitHub down), SHELFCODE starts the version it has. To turn this off, set `SHELFCODE_AUTO_UPDATE=0` in `/etc/shelfcode/shelfcode.env`, or untick it in Settings → About. `.deb`/`.rpm` installs are never touched; update those with apt/dnf.

## Everyday commands

```bash
systemctl status shelfcode
```

```bash
journalctl -u shelfcode -f
```

```bash
sudo systemctl restart shelfcode
```

## Settings

Edit `/etc/shelfcode/shelfcode.env`, then `sudo systemctl restart shelfcode`.

| Variable | Default | Meaning |
|---|---|---|
| `SHELFCODE_PORT` | `8765` | web port |
| `SHELFCODE_HOST` | `0.0.0.0` | listen address. Use `127.0.0.1` behind a reverse proxy. |
| `SHELFCODE_PIN` | *(none)* | PIN required on every device. If set here, it wins over the PIN set in the app. |
| `SHELFCODE_DATA` | `/var/lib/shelfcode` | data folder. If you change it, also add `ReadWritePaths=` (see below). |
| `TZ` | server's | time zone for dates and the daily backup |

The same options exist as flags: `shelfcode --help`.

**Security:** without a PIN, anyone who can reach the port can use SHELFCODE. Set a PIN, or keep the port on your LAN / behind a VPN or reverse proxy with authentication. On a server, every logged-in device can change all settings. There is no separate "main PC".

### Data in another folder

```bash
sudo systemctl edit shelfcode
```

Add:

```ini
[Service]
Environment=SHELFCODE_DATA=/srv/shelfcode
ReadWritePaths=/srv/shelfcode
```

Then create the folder, give it to the service user, and restart:

```bash
sudo install -d -o shelfcode -g shelfcode -m 0750 /srv/shelfcode
```

```bash
sudo systemctl restart shelfcode
```

The same applies to the optional "also copy backups to" folder in Settings → Backup: that folder must also be listed in `ReadWritePaths=`.

## HTTPS / reverse proxy

SHELFCODE speaks plain HTTP. For HTTPS, put it behind your usual proxy and set `SHELFCODE_HOST=127.0.0.1`.

**Caddy:**

```
inventory.example.com {
    reverse_proxy 127.0.0.1:8765
}
```

**nginx:** scans are pushed to the page over a long-lived stream, so turn off buffering:

```nginx
location / {
    proxy_pass http://127.0.0.1:8765;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 1h;
    client_max_body_size 100m;
}
```

## Backups

SHELFCODE writes a `.zip` backup (database + photos) to `/var/lib/shelfcode/backups` once a day, and before every import, restore and upgrade. Settings → Backup can make, download and restore them. For off-site copies, back up `/var/lib/shelfcode` with your usual tool. The `backups/` zips are consistent snapshots, so copying them is safe while SHELFCODE runs.

## The service

`shelfcode.service` runs with `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `NoNewPrivileges` and other hardening. It can only write to its data folder.
