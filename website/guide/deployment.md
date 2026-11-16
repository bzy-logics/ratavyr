# Deployment & security

ratavyr is the Go server that puts a REST/WebSocket API, authentication and an audit log in front of the engine. On a development machine it runs with no configuration. Before anyone else can reach it, configure it as described here.

## Build and run

```bash
go build -o ratavyr ./cmd/server
./ratavyr
```

All settings are environment variables. See the [environment variable reference](/reference/environment) for the full list.

## Authentication

Set all three role tokens (`MOTIONCORE_VIEWER_TOKEN`, `MOTIONCORE_OPERATOR_TOKEN`, `MOTIONCORE_API_TOKEN`) to long random values. Without any token, authentication is off and ratavyr only listens on loopback. Details: [Authentication & roles](/guide/authentication).

## Network exposure

```text
browser ──HTTPS──▶ reverse proxy (TLS) ──HTTP──▶ ratavyr 127.0.0.1:8080 ──gRPC, local only──▶ engine
```

- **TLS:** ratavyr speaks plain HTTP. Put it behind a TLS-terminating reverse proxy, and set tokens.
- **Engine link:** the engine's gRPC API has no authentication. Keep it on a Unix socket (Linux default) or loopback. Never expose it on a network.
- **Proxying:** the proxy must forward WebSocket upgrades on `/api/ws`.

### Browser protections

These protect an operator whose browser has other sites open:

- **Origin check.** A `POST` or WebSocket with an `Origin` header must be same-origin or match `MOTIONCORE_ALLOWED_ORIGINS` (default `localhost:5173,127.0.0.1:5173` for the Vite dev server).
- **Content-type check.** `POST`s with `text/plain`, `application/x-www-form-urlencoded` or `multipart/form-data` are rejected with `415`, because browsers can send those cross-site without a preflight.
- **DNS-rebinding protection.** While authentication is off, only loopback `Host` names are served. Add others (e.g. `robot.lan:*` behind a local proxy) with `MOTIONCORE_ALLOWED_HOSTS`.

Without these, any web page open in the operator's browser could send commands to a local, unauthenticated server.

## Abuse limits

- `POST` requests are rate-limited per client IP: `MOTIONCORE_RATE_LIMIT` per second (default 20), burst `MOTIONCORE_RATE_BURST` (default 40). **`stop` and `estop` are never limited.**
- 10 failed authentications within a minute block a client IP (`429`), even with a correct token, until the window passes.
- Request bodies are capped at 64 KiB; unknown JSON fields are rejected.

## Audit log

Every command (with its parameters), firmware build and flash, WebSocket ticket and rejected authentication is recorded as a JSON line with role, client IP, outcome and duration. By default it goes to the main log; set `MOTIONCORE_AUDIT_LOG` to write it to a file.

## Serving the UI

Set `MOTIONCORE_WEB_DIR` to [sindrava](https://sindrava.bzy-logics.com)'s built `dist/` directory. ratavyr serves it at `/` with SPA fallback, on the same origin as the API, with security headers: `Content-Security-Policy`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.

## Reliability

- ratavyr supervises the engine connection: a `GetInfo` handshake (engine `api_version` major must be 1), then `StreamEvents`. On loss it reconnects with exponential backoff from 250 ms to 5 s.
- Every engine call has a 5 s deadline (`504 engine_timeout`).
- While the engine is unavailable, API calls fail fast with `503 engine_unavailable`, WebSocket clients get `{"type":"engine","data":{"connected":false}}`, and `/api/info` explains why in `engine_error`.
- Slow WebSocket clients are disconnected (close code 1013) rather than holding up telemetry for everyone.

## Checklist

- [ ] All three role tokens set, long and random
- [ ] TLS-terminating reverse proxy in front, forwarding WebSocket upgrades
- [ ] Engine on a Unix socket or loopback only
- [ ] `MOTIONCORE_ALLOWED_ORIGINS` set to your public origin if the UI is served from elsewhere
- [ ] `MOTIONCORE_AUDIT_LOG` set to a persistent file
- [ ] Hardware E-stop wired and tested
