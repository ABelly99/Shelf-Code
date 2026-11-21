# SHELFCODE with Docker

The image is `ghcr.io/abelly99/shelf-code`. It has tags `latest`, `1`, `1.0` and `1.0.0`, and is built for amd64, arm64 and armv7. It is a single static program with no shell, its data is in `/data`, and the port is `8765`.

## docker run

```bash
docker run -d --name shelfcode --restart unless-stopped -p 8765:8765 -v shelfcode-data:/data -e TZ=America/Toronto ghcr.io/abelly99/shelf-code:latest
```

Open `http://<this-machine>:8765`.

## Docker Compose

[`deploy/docker-compose.yml`](../deploy/docker-compose.yml):

```yaml
services:
  shelfcode:
    image: ghcr.io/abelly99/shelf-code:latest
    container_name: shelfcode
    restart: unless-stopped
    ports:
      - "8765:8765"
    volumes:
      - ./shelfcode-data:/data
    environment:
      TZ: UTC
      # SHELFCODE_PIN: "1234"
```

```bash
docker compose up -d
```

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `SHELFCODE_PIN` | *(none)* | PIN required on every device |
| `TZ` | `UTC` | time zone for dates and the daily backup |
| `SHELFCODE_PORT` | `8765` | port inside the container. Usually you change the published port instead (`-p 9000:8765`). |

## Update

Restarting the container installs the newest release automatically (`docker restart shelfcode`). The new program is kept in the data volume (`/data/.app`), so it survives restarts. Turn this off with `SHELFCODE_AUTO_UPDATE=0`. To update the image itself:

```bash
docker compose pull && docker compose up -d
```

Your data in the volume is kept. SHELFCODE backs up the database before upgrading it.

## Health

The image has a built-in health check (`/shelfcode healthcheck`), so `docker ps` shows `healthy`. Logs: `docker logs -f shelfcode`.

## Build it yourself

```bash
docker build -t shelfcode --build-arg VERSION=dev .
```
