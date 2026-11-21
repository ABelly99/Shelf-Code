## Download

| You have | Download | Then |
|---|---|---|
| **Windows PC** | [`SHELFCODE.exe`](https://github.com/ABelly99/Shelf-Code/releases/download/v{{VERSION}}/SHELFCODE.exe) | Put it in an empty folder and double-click it. It sets up everything (data folder, database, backups, desktop shortcut) and opens your browser. |
| **Linux server** | one command | `curl -fsSL https://github.com/ABelly99/Shelf-Code/releases/download/v{{VERSION}}/install.sh \| sudo bash` |
| **Debian / Ubuntu** | `shelfcode_{{VERSION}}_amd64.deb` (or `arm64`, `armhf`) | `sudo apt install ./shelfcode_{{VERSION}}_amd64.deb` |
| **Fedora / RHEL / Rocky** | `shelfcode-{{VERSION}}-1.x86_64.rpm` (or `aarch64`, `armv7hl`) | `sudo dnf install --nogpgcheck ./shelfcode-{{VERSION}}-1.x86_64.rpm` |
| **ZimaOS / CasaOS** | [`shelfcode-zimaos.yml`](https://github.com/ABelly99/Shelf-Code/releases/download/v{{VERSION}}/shelfcode-zimaos.yml) | App Store → **+** → Custom Install → Import → upload the file → Install. [Step by step](https://github.com/ABelly99/Shelf-Code/blob/main/docs/zimaos.md) |
| **Docker** | `ghcr.io/abelly99/shelf-code:{{VERSION}}` | `docker run -d -p 8765:8765 -v shelfcode-data:/data --restart unless-stopped ghcr.io/abelly99/shelf-code:{{VERSION}}` |

Then open `http://<that-machine>:8765` in a browser.

Other files: `SHELFCODE-arm64.exe` (Windows on ARM), `shelfcode-linux-*.tar.gz` (program + installer + systemd service, for manual installs), `shelfcode-zimaos-appstore.zip` (ZimaOS app source), `checksums.txt` (SHA-256).

Guides: [Linux server](https://github.com/ABelly99/Shelf-Code/blob/main/docs/linux.md) · [Docker](https://github.com/ABelly99/Shelf-Code/blob/main/docs/docker.md) · [ZimaOS](https://github.com/ABelly99/Shelf-Code/blob/main/docs/zimaos.md)
