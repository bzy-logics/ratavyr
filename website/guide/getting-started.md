# Getting started

## Requirements

- Go, at the version in `go.mod`. Code-generation tools are pinned in `go.mod` and run with `go tool`; nothing else needs installing.
- A running MotionCore engine. For development, the engine with its virtual MCU is enough — no hardware.

## 1. Start the engine

From a [motioncore](https://github.com/bzy-logics/motioncore) checkout:

```sh
cargo build
target/debug/motioncore start --config robot-profiles/single-axis-virtual.toml
```

The engine listens on `unix:///run/motioncore/engine.sock` on Linux and `127.0.0.1:7600` on Windows and macOS. See the [motioncore quick start](https://motioncore.bzy-logics.com/guide/quick-start).

## 2. Run ratavyr

```sh
go run ./cmd/server                 # API on http://127.0.0.1:8080
# or
go build -o ratavyr ./cmd/server && ./ratavyr
```

ratavyr connects to the engine at the platform default address. If the engine listens elsewhere, set `MOTIONCORE_ENGINE_ADDR`:

```sh
MOTIONCORE_ENGINE_ADDR=127.0.0.1:7600 go run ./cmd/server
MOTIONCORE_ENGINE_ADDR=unix:///run/motioncore/engine.sock go run ./cmd/server
```

With no tokens set, **authentication is off**: every request is treated as admin, and ratavyr refuses to listen on anything but loopback. That's fine for local development. Read [Deployment & security](/guide/deployment) before anyone else can reach it.

## 3. Try the API

```sh
curl http://127.0.0.1:8080/api/health
# {"status":"ok","engine":{"connected":true}}

curl http://127.0.0.1:8080/api/info
curl http://127.0.0.1:8080/api/status

curl -X POST http://127.0.0.1:8080/api/commands/enable
curl -X POST -H "Content-Type: application/json" -d '{"axis":"j1"}'          http://127.0.0.1:8080/api/commands/home
curl -X POST -H "Content-Type: application/json" -d '{"axis":"j1","delta":5}' http://127.0.0.1:8080/api/commands/jog
curl -X POST http://127.0.0.1:8080/api/commands/stop
```

A `{}` response means the engine accepted the command. Watch `/api/status` or the [WebSocket](/reference/websocket) for the result.

## 4. Add the UI (optional)

Run [sindrava](https://sindrava.bzy-logics.com/guide/getting-started)'s dev server, which proxies `/api` to `127.0.0.1:8080`:

```sh
cd ../sindrava && npm install && npm run dev     # http://localhost:5173
```

For production, build the UI and let ratavyr serve it:

```sh
MOTIONCORE_WEB_DIR=../sindrava/dist ./ratavyr
```

## Setting the reported version

`/api/info` reports `server_version`. Set it at link time:

```sh
go build -ldflags "-X github.com/bzy-logics/ratavyr/internal/api.ServerVersion=0.3.1" -o ratavyr ./cmd/server
```
