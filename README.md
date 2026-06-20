# ratavyr

Go management server for MotionCore. It talks to the Rust engine
(`motioncore` repo) over gRPC (`motioncore.engine.v1`) and exposes the
REST/WebSocket API used by `sindrava`, specified in
[`docs/rest-api.md`](docs/rest-api.md) (the source of truth for the HTTP API).

The engine is the single authority for robot state. This server keeps no
robot state machine; it relays engine responses and caches only the latest
telemetry frame for new WebSocket clients.

## Run

```sh
go run ./cmd/server                 # or: go build -o ratavyr ./cmd/server
go vet ./... && go test ./...
```

## Environment

| variable | default | meaning |
|---|---|---|
| `MOTIONCORE_ENGINE_ADDR` | Linux: `unix:///run/motioncore/engine.sock`; Windows/macOS: `127.0.0.1:7600` | engine gRPC address: a gRPC target (`unix:///path`, `dns:///host:port`, ...) or plain `host:port` (insecure, local only) |
| `MOTIONCORE_API_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `MOTIONCORE_API_TOKEN` | – | if set, `/api/*` (except `/api/health`, `/api/info`) requires `Authorization: Bearer <token>`; `/api/ws` also accepts `?access_token=<token>`. If unset, the server refuses to start on a non-loopback address. |
| `MOTIONCORE_WEB_DIR` | – | optional built web UI, served at `/` with SPA fallback to `index.html` |
| `MOTIONCORE_ALLOWED_ORIGINS` | `localhost:5173,127.0.0.1:5173` | extra WebSocket origin host patterns (same-origin is always allowed) |

On connect the server calls `GetInfo` and refuses engines whose `api_version`
major is not 1 (logged as an error, reported as `engine_unavailable`). It then
subscribes to `StreamEvents` and reconnects with backoff (250 ms – 5 s) if
the engine goes away. Engine calls time out after 5 s (`engine_timeout`).

## Layout

```
cmd/server/          main (binary ratavyr)
internal/api/        router, /api/health, /api/info, static files
internal/api/rest/   REST JSON types, proto -> REST conversion, error mapping
internal/auth/       bearer-token middleware
internal/engine/     gRPC client: handshake, event stream, fanout, Call helper
internal/engine/enginetest/  in-process fake gRPC engine for tests
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
(`engine-api/v1.0.0`), proto path and its sha256. No copy of the proto is
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
