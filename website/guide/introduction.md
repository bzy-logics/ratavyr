# Introduction

ratavyr is the Go management server for [MotionCore](https://motioncore.bzy-logics.com). It talks to the Rust engine over gRPC (`motioncore.engine.v1`) and exposes the REST/WebSocket API used by the [sindrava](https://sindrava.bzy-logics.com) operator UI and any other HTTP client.

```text
sindrava ──REST / WebSocket──▶ ratavyr ──gRPC──▶ motioncore engine ──MCU protocol──▶ MCU / virtual MCU
```

## What it does

- **Authenticates and authorizes** every request against per-role tokens.
- **Relays** commands to the engine and telemetry back to clients.
- **Fans out** live telemetry and logs to any number of WebSocket clients.
- **Audits** every command, firmware job, WebSocket ticket and rejected authentication.
- **Orchestrates firmware jobs** by delegating builds and flashes to the engine's restricted builder.
- Optionally **serves the built web UI** on the same origin as the API.

## What it never does

The engine is the single authority for robot state, motion planning and safety. ratavyr:

- keeps **no robot state machine** — it caches only the latest telemetry frame, so a newly connected WebSocket client sees something immediately;
- never plans motion, schedules steps, or talks to an MCU;
- never fakes functionality the engine doesn't have (see [limitations](/development/layout#known-limitations)).

A `200` from a command means the engine **accepted** it, not that it finished. Confirmation comes through telemetry — for an E-stop, the `emergency_stopped` state.

## Is it required?

No. The engine and its `motioncore` CLI work on their own. Use ratavyr when you want a browser UI, remote access with roles, or an audit trail. Any HTTP client can use the API; sindrava is just one.

## Compatibility

| Peer | Requirement |
|---|---|
| motioncore engine | engine-api **1.x** (`api_version` major must be 1); checked at every connect |
| Clients | REST API **v1**, specified in [`docs/rest-api.md`](https://github.com/bzy-logics/ratavyr/blob/main/docs/rest-api.md) |

## Next

- [Getting started](/guide/getting-started)
- [Authentication & roles](/guide/authentication)
- [REST API reference](/reference/rest-api)
