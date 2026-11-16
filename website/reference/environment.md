# Environment variables

All configuration is read from the environment at start.

| Variable | Default | Meaning |
|---|---|---|
| `MOTIONCORE_ENGINE_ADDR` | Linux: `unix:///run/motioncore/engine.sock`; Windows/macOS: `127.0.0.1:7600` | Engine gRPC address: a gRPC target (`unix:///path`, `dns:///host:port`, …) or plain `host:port` (insecure, local only) |
| `MOTIONCORE_API_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `MOTIONCORE_API_TOKEN` | – | **admin** token: full access, including firmware flashing |
| `MOTIONCORE_OPERATOR_TOKEN` | – | **operator** token: motion and drive commands, firmware builds |
| `MOTIONCORE_VIEWER_TOKEN` | – | **viewer** token: read-only plus `stop` / `estop` |
| `MOTIONCORE_ALLOWED_ORIGINS` | `localhost:5173,127.0.0.1:5173` | Extra browser origins (host patterns) allowed for the WebSocket and `POST`s. Same-origin is always allowed |
| `MOTIONCORE_ALLOWED_HOSTS` | – | Extra `Host` patterns accepted while authentication is off (e.g. `robot.lan:*` behind a local proxy) |
| `MOTIONCORE_AUDIT_LOG` | – (main log) | File for JSON-lines audit records |
| `MOTIONCORE_RATE_LIMIT` | `20` | `POST` requests per second per client IP; `stop` and `estop` are never limited |
| `MOTIONCORE_RATE_BURST` | `40` | Burst for the above |
| `MOTIONCORE_WEB_DIR` | – | Built web UI to serve at `/`, with SPA fallback and security headers |

If no token is set, authentication is off and ratavyr refuses to start on a non-loopback address. Tokens must differ from each other; tokens shorter than 16 characters are accepted with a warning.

## Development and tests

| Variable | Used by | Meaning |
|---|---|---|
| `MOTIONCORE_REPO` | `scripts/generate.sh`, integration tests | Path to a motioncore checkout (default `../motioncore`) |
| `MOTIONCORE_BIN` | integration tests | Path to the `motioncore` binary |
