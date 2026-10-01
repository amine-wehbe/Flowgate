# Flowgate

A self-hosted HTTP/HTTPS intercepting proxy with a live inspection UI. Inspired by Burp Suite/Charles Proxy, built from scratch.

![Flowgate capturing HTTPS traffic](docs/screenshots/1-overview.png)

## Stack

- **Proxy** — Go, handles HTTP and HTTPS (TLS MITM via local CA)
- **API** — Go, REST + WebSocket
- **Database** — PostgreSQL
- **Frontend** — React + Vite, served by nginx in Docker

## Features

- Intercept HTTP and HTTPS traffic transparently
- Live request feed via WebSocket — new requests appear instantly
- Full request/response detail: headers, body, status, timing, TLS flag
- Replay any captured request directly from the UI
- Clear history with one click
- Dark theme, two-panel layout

## Quick Start

**Prerequisites:** Docker Desktop, mkcert installed and CA trusted (`mkcert -install`)

```bash
git clone https://github.com/amine-wehbe/flowgate
cd flowgate
cp .env.example .env   # then fill in your DB credentials and MKCERT_CAROOT
docker-compose up --build
```

- Frontend: http://localhost:4000
- API: http://localhost:3000
- Proxy: localhost:8080

## Configure Your Browser

To capture browser traffic, set your system proxy to `localhost:8080` for both HTTP and HTTPS:

**Mac:** System Settings → Network → Wi-Fi → Details → Proxies
- Web Proxy (HTTP): `localhost:8080`
- Secure Web Proxy (HTTPS): `localhost:8080`

Remember to disable the proxy when done!! As to not log all of your traffic.

## Configure mkcert

The proxy signs a certificate for each intercepted host using your mkcert root CA. Point `MKCERT_CAROOT` in `.env` at your mkcert directory:

```bash
mkcert -CAROOT   # prints the path to put in .env
```

Docker Compose mounts that directory read-only into the proxy container. The proxy refuses to start if `MKCERT_CAROOT` is not set.

## Reset Database

```bash
docker-compose down -v && docker-compose up
```

## Local Development (without Docker)

```bash
# Terminal 1 — DB only
docker-compose up db

# Terminal 2 — API
cd api && DATABASE_URL=postgres://user:pass@localhost:5432/db go run .

# Terminal 3 — Proxy
cd proxy && MKCERT_CAROOT="$(mkcert -CAROOT)" go run .

# Terminal 4 — Frontend
cd frontend && npm run dev
```

Frontend at http://localhost:5173

## Architecture

```
Browser → Proxy (:8080) → API (:3000) → PostgreSQL
                                ↓
                         WebSocket hub
                                ↓
                       React UI (:4000)
```

The proxy intercepts all traffic, logs it to the API, which persists to PostgreSQL and broadcasts to all connected WebSocket clients in real time.
