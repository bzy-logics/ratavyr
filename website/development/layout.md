# Code layout & limitations

## Layout

```text
cmd/server/                    main (binary ratavyr)
internal/api/                  router, /api/health, /api/info, /api/session, /api/ws-tickets, static files
internal/api/rest/             REST JSON types, proto → REST conversion, error mapping
internal/auth/                 tokens, roles, WebSocket tickets, failed-auth blocking
internal/guard/                Origin / Content-Type / Host checks, POST rate limit
internal/audit/                structured audit records
internal/engine/               gRPC client: handshake, event stream, fan-out, Call helper
internal/engine/enginetest/    in-process fake gRPC engine for tests
internal/integration/          tests against the real engine (build tag "integration")
internal/robots/               /api/status, /api/config, /api/commands/{name}
internal/plugins/              /api/plugins
internal/firmware/             /api/firmware/* → FirmwareService
internal/websocket/            /api/ws
internal/config/               environment configuration
gen/                           generated gRPC bindings (committed, do not edit)
docs/rest-api.md               HTTP API specification (source of truth)
website/                       this documentation site
```

## Engine connection

`internal/engine` supervises the connection:

- **Handshake:** `GetInfo`; the engine's `api_version` major must be 1.
- **Stream:** `StreamEvents`, fanned out to WebSocket clients. Only the latest telemetry frame is cached.
- **Reconnect:** exponential backoff from 250 ms to 5 s.
- **Deadlines:** every engine call has 5 s (`504 engine_timeout`).
- **Fail fast:** while disconnected, API calls return `503 engine_unavailable` immediately, and `/api/info` reports the reason in `engine_error` (for example an incompatible `api_version`).

## Known limitations

These need new engine functionality first. ratavyr does not fake them.

- **Configuration editing / validation API.** The engine only exposes `GetRobotConfig`; robot profiles are TOML files loaded at engine start.
- **Firmware verification** is CLI-only (`motioncore firmware verify`).
- **Firmware job progress** is polled (`GET /api/firmware/jobs/{id}`); the engine's event stream does not carry job updates.
- **One MCU per robot** in telemetry (engine-api v1).
- **Coordinated multi-axis motion** is not offered by the engine yet; commands address one axis at a time.
- **Roles are per token.** There are no user accounts, and tokens can only be rotated by restarting.
