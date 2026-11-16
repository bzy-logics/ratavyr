# ratavyr

Go management server for MotionCore. It talks to the Rust engine
([motioncore](https://github.com/bzyfuzy/motioncore)) over gRPC
(`motioncore.engine.v1`) and exposes the REST/WebSocket API used by
[sindrava](https://github.com/bzyfuzy/sindrava) and other clients, specified
in [`docs/rest-api.md`](docs/rest-api.md) (the source of truth for the HTTP
API).

The engine is the single authority for robot state, motion planning and
safety. This server is a gateway: it authenticates and authorizes requests,
relays them, and audits them. It keeps no robot state machine and caches only
the latest telemetry frame for new WebSocket clients.

```text
sindrava ──REST / WebSocket──▶ ratavyr ──gRPC──▶ motioncore engine ──MCU protocol──▶ MCU / virtual MCU
```

## Run

```sh
go run ./cmd/server                 # or: go build -o ratavyr ./cmd/server
```

With the engine and its simulator (from a motioncore checkout):

```sh
motioncore start --config robot-profiles/single-axis-virtual.toml   # engine on 127.0.0.1:7600 (Windows/macOS)
go run ./cmd/server                                                 # API on 127.0.0.1:8080
```

## Environment

| variable | default | meaning |
|---|---|---|
| `MOTIONCORE_ENGINE_ADDR` | Linux: `unix:///run/motioncore/engine.sock`; Windows/macOS: `127.0.0.1:7600` | engine gRPC address: a gRPC target (`unix:///path`, `dns:///host:port`, ...) or plain `host:port` (insecure, local only) |
| `MOTIONCORE_API_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `MOTIONCORE_API_TOKEN` | – | **admin** token (full access, including firmware flashing) |
| `MOTIONCORE_OPERATOR_TOKEN` | – | **operator** token: motion and drive commands, firmware builds |
| `MOTIONCORE_VIEWER_TOKEN` | – | **viewer** token: read-only plus `stop`/`estop` |
| `MOTIONCORE_ALLOWED_ORIGINS` | `localhost:5173,127.0.0.1:5173` | extra browser origins (host patterns) allowed for the WebSocket and for `POST` requests; same-origin is always allowed |
| `MOTIONCORE_ALLOWED_HOSTS` | – | extra `Host` header patterns accepted while authentication is off (e.g. `robot.lan:*` behind a local proxy) |
| `MOTIONCORE_AUDIT_LOG` | – | file for JSON-lines audit records; default: the main log |
| `MOTIONCORE_RATE_LIMIT` / `MOTIONCORE_RATE_BURST` | `20` / `40` | `POST` requests per second and burst, per client IP; `stop` and `estop` are never limited |
| `MOTIONCORE_WEB_DIR` | – | optional built web UI, served at `/` with SPA fallback and security headers |

If no token is set, authentication is off and the server refuses to start on
a non-loopback address. Tokens must differ from each other; tokens shorter
than 16 characters are accepted with a warning.

## Security

* **Roles.** viewer < operator < admin, enforced on every route (see
  [docs/rest-api.md](docs/rest-api.md#authentication-and-authorization)).
  `GET /api/session` tells clients their role so they can hide what they
  cannot do; the server enforces it regardless.
* **WebSocket tickets.** Clients exchange their token for a 30-second,
  single-use ticket (`POST /api/ws-tickets`) and connect with
  `/api/ws?ticket=…`, so long-lived tokens never appear in URLs.
  `?access_token=` still works but is deprecated and logged.
* **Cross-site and DNS-rebinding protection.** Cross-origin `POST`s and the
  "simple" content types browsers can send cross-site are rejected. While
  authentication is off, only loopback `Host` names (or allowed hosts) are
  served. Without this, any web page open in the operator's browser could
  send commands to a local, unauthenticated server.
* **Brute force.** 10 failed authentications per minute block a client IP.
* **Rate limits** on commands, with `stop`/`estop` exempt.
* **Audit log.** Every command (with its parameters), firmware build and
  flash, WebSocket ticket and rejected authentication is recorded with role,
  client IP, outcome and duration.
* **Transport.** The server speaks plain HTTP. Across a network, put it
  behind a TLS-terminating reverse proxy, and set tokens. The engine link is
  unauthenticated gRPC and must stay on a Unix socket or loopback.
* **Safety.** A software emergency stop is relayed like any other command.
  It never replaces a hardware E-stop circuit that removes driver power. A
  `200` response means the engine accepted the command; confirmation is the
  `emergency_stopped` state in telemetry.

## Reliability

* The engine connection is supervised: `GetInfo` handshake (engine
  `api_version` major must be 1), then `StreamEvents`; on loss it reconnects
  with exponential backoff (250 ms – 5 s). Every engine call has a 5 s
  deadline (`engine_timeout`).
* While the engine is unavailable, API calls fail fast with 503
  `engine_unavailable`, WebSocket clients get `{"type":"engine","data":
  {"connected":false}}`, and `/api/info` reports the reason in
  `engine_error` (e.g. an incompatible `api_version`).
* Slow WebSocket clients are disconnected (close code 1013) instead of
  blocking telemetry for everyone.

## Tests

```sh
gofmt -l .                    # must print nothing
go vet ./...
go test ./...                 # unit + API tests against an in-process fake engine
go test -race ./...           # needs cgo (CI runs it on Linux)
```

Integration tests run against the **real** engine with its virtual MCU
(enable, home, jog, move, stop, E-stop, fault injection and reset, engine
loss and recovery, firmware API, roles):

```sh
(cd ../motioncore && cargo build -p motioncore-cli)
MOTIONCORE_BIN=../motioncore/target/debug/motioncore MOTIONCORE_REPO=../motioncore \
  go test -tags integration -v ./internal/integration/
```

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs formatting,
vet, race tests, the contract check and the integration tests.

## Layout

```
cmd/server/          main (binary ratavyr)
internal/api/        router, /api/health, /api/info, /api/session, /api/ws-tickets, static files
internal/api/rest/   REST JSON types, proto -> REST conversion, error mapping
internal/auth/       tokens, roles, WebSocket tickets, failed-auth blocking
internal/guard/      Origin / Content-Type / Host checks, POST rate limit
internal/audit/      structured audit records
internal/engine/     gRPC client: handshake, event stream, fanout, Call helper
internal/engine/enginetest/  in-process fake gRPC engine for tests
internal/integration/        tests against the real engine (build tag "integration")
internal/robots/     /api/status, /api/config, /api/commands/{name}
internal/plugins/    /api/plugins
internal/firmware/   /api/firmware/* -> FirmwareService
internal/websocket/  /api/ws
internal/config/     environment configuration
gen/                 generated gRPC bindings (committed, do not edit)
```

## Engine contract and generated code

Compatibility: **engine-api 1.x** (`motioncore.engine.v1`). The contract is
pinned in [`contract.lock`](contract.lock): source repo, tag
(`engine-api/v1.0.1`), commit, proto path and its sha256. No copy of the proto is
kept in this repo.

```sh
scripts/generate.sh          # regenerate gen/
scripts/check-contract.sh    # verify gen/ matches contract.lock (CI)
```

The script reads the proto with `git show <tag>:<path>` from a local clone of
the motioncore repo (`$MOTIONCORE_REPO`, default `../motioncore`), verifies
the sha256, writes it to `.contract/` (gitignored) and runs
`go tool buf generate` ([`buf.gen.yaml`](buf.gen.yaml)). `buf`,
`protoc-gen-go` and `protoc-gen-go-grpc` are pinned in `go.mod` via the
`tool` directive and run with `go tool`; nothing needs to be installed.

To move to a newer contract (e.g. `engine-api/v1.1.0`): fetch the tag in the
motioncore clone, update `tag`, `commit` and `sha256` in `contract.lock`
(`git -C ../motioncore show <tag>:<path> | sha256sum`), run
`scripts/generate.sh`, then map any new fields in `internal/api/rest` and
`docs/rest-api.md`. A new major version (`motioncore.engine.v2`) is a
separate package and requires code changes.

## Known limitations and engine dependencies

These need new engine functionality first; this server does not fake them:

* **Configuration editing / validation API.** The engine only exposes
  `GetRobotConfig`; robot profiles are edited as TOML files and loaded at
  engine start.
* **Firmware verification** is CLI-only (`motioncore firmware verify`).
* **Firmware job progress** is polled (`GET /api/firmware/jobs/{id}`); the
  engine's event stream does not carry job updates.
* **One MCU per robot** in telemetry (engine-api v1).
* **Coordinated multi-axis motion** is not offered by the engine yet;
  commands address one axis at a time.
* Roles are per token; there are no user accounts or token rotation without a
  restart.
