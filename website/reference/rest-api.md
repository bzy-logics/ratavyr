# REST & WebSocket API (v1)

Served by **ratavyr**, consumed by sindrava and any other HTTP client. ratavyr translates between this API and the engine's [gRPC contract](https://motioncore.bzy-logics.com/reference/engine-api). It never derives robot state; every robot field is relayed from the engine.

::: info Source of truth
The canonical specification is [`ratavyr/docs/rest-api.md`](https://github.com/bzy-logics/ratavyr/blob/main/docs/rest-api.md). sindrava mirrors it type-for-type in `src/types/api.ts`.
:::

## Conventions

- JSON with `snake_case` keys.
- Enums are lowercase strings without prefix: `ROBOT_STATE_EMERGENCY_STOPPED` → `"emergency_stopped"`.
- Unset optional values are `null`, never omitted.
- Units are the axis display units (`deg` / `mm`).
- Errors: an HTTP status plus `{"error": {"code": "...", "message": "..."}}`. See [error codes](/reference/errors).
- Every response carries `Cache-Control: no-store`.
- Unknown enum values from a newer 1.x engine arrive as `"unknown"`; `*_UNSPECIFIED` as `"unspecified"`. Clients must tolerate both.

## Authentication

See [Authentication & roles](/guide/authentication). In short: send `Authorization: Bearer <token>`. Once any token is configured, every endpoint except `GET /api/health` and `GET /api/info` requires one.

| Role | Token variable | May |
|---|---|---|
| `viewer` | `MOTIONCORE_VIEWER_TOKEN` | Read everything, WebSocket, `stop`, `estop` |
| `operator` | `MOTIONCORE_OPERATOR_TOKEN` | Viewer + all other commands, firmware builds |
| `admin` | `MOTIONCORE_API_TOKEN` | Operator + firmware flashing |

- Missing or wrong token → `401 unauthorized` with `WWW-Authenticate: Bearer`.
- An action above the caller's role → `403 forbidden`.
- 10 failed authentications within a minute → `429 rate_limited` for that client IP, even with a correct token, until the window passes.
- **No token configured** → authentication off; every request is treated as `admin`, and the server only listens on loopback.

## Request rules

- `POST` bodies must be `application/json` or have no content type. `text/plain`, `application/x-www-form-urlencoded` and `multipart/form-data` → `415 unsupported_media_type`.
- A browser `Origin` must be same-origin or match `MOTIONCORE_ALLOWED_ORIGINS`, otherwise `403`.
- While authentication is off, `Host` must be `localhost`, a loopback IP, or match `MOTIONCORE_ALLOWED_HOSTS`, otherwise `403`.
- `POST`s are rate-limited per client IP (default 20/s, burst 40) → `429` with `Retry-After`. **`stop` and `estop` are never limited.**
- Bodies are at most 64 KiB; unknown JSON fields are rejected.
- Axis ids are 1–64 characters of letters, digits, `_`, `-` and `.`.

## Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/health` | – | `{"status":"ok","engine":{"connected":bool}}` |
| GET | `/api/info` | – | `{"server_version":"0.3.0","auth_required":bool,"engine":EngineInfo\|null,"engine_error":string\|null}` |
| GET | `/api/session` | – | `Session` |
| POST | `/api/ws-tickets` | `{}` | `201` `{"ticket":string,"expires_in_ms":30000}` |
| GET | `/api/status` | – | `Telemetry` |
| GET | `/api/config` | – | `RobotConfig` |
| GET | `/api/plugins` | – | `Plugin[]` |
| POST | `/api/commands/{name}` | params (may be `{}`) | `{}` |
| GET | `/api/firmware/targets` | – | `FirmwareTarget[]` |
| GET | `/api/firmware/jobs` | – | `FirmwareJob[]`, newest first |
| GET | `/api/firmware/jobs/{id}` | – | `FirmwareJob` |
| POST | `/api/firmware/builds` | `{"mcu": string?}` | `202` `FirmwareJob` |
| POST | `/api/firmware/flashes` | `{"build_job_id": string, "confirm": true}` | `202` `FirmwareJob` |
| GET | `/api/ws` | – | WebSocket (below) |

`engine_error` in `/api/info` explains why the engine is not connected — for example `incompatible engine api_version "2.0.0" (this server requires 1.x)` — and is `null` while connected.

### Commands

`POST /api/commands/{name}`

| Name | Params | Minimum role |
|---|---|---|
| `stop`, `estop` | none | viewer |
| `enable`, `disable`, `reset_fault` | none | operator |
| `jog` | `{"axis":"j1","delta":5.0,"velocity"?:30.0}` | operator |
| `move_to` | `{"axis":"j1","position":-90.0,"velocity"?:30.0}` | operator |
| `home` | `{"axis":"j1"}` | operator |

A `{}` response means the engine **accepted** the command, not that it has finished. For `estop`, wait for telemetry with `"state": "emergency_stopped"` before showing the stop as confirmed. A software emergency stop never replaces a hardware E-stop circuit.

```bash
TOKEN=...
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d '{"axis":"j1","delta":5}' http://127.0.0.1:8080/api/commands/jog
```

### Firmware

- `POST /api/firmware/builds` needs **operator**; `POST /api/firmware/flashes` needs **admin**.
- Always send `mcu` when building; omitting it only works when the robot has exactly one MCU.
- Building for a virtual board → `400 bad_request`.
- Flashing without `"confirm": true` → `400 bad_request` (checked before the engine is contacted). Unknown job → `404 not_found`. A job that is not a succeeded build → `400 bad_request`. Drives energised or another job running → `409 invalid_state`.
- Progress is polled with `GET /api/firmware/jobs/{id}`.
- Firmware verification is CLI-only; there is no endpoint.

## WebSocket

Live telemetry and logs: see [WebSocket](/reference/websocket).

## Types

### Session

```jsonc
{"auth_required": true, "role": "operator",            // viewer | operator | admin
 "permissions": {"view": true, "stop": true, "command": true,
                 "firmware_build": true, "firmware_flash": false}}
// Without configured tokens: {"auth_required": false, "role": "admin", all permissions true}
```

### EngineInfo

```json
{"api_version": "1.0.0", "engine_version": "0.2.0", "mcu_protocol_version": 1,
 "plugin_api_version": "1", "fault_injection_enabled": false}
```

### Telemetry

```jsonc
{
  "seq": 1042,                 // monotonic per engine process; gaps = dropped samples
  "time_ms": 52100,            // since engine start
  "state": "ready",            // disconnected|initializing|disabled|ready|homing|running|faulted|emergency_stopped
  "fault": null,               // or {"code": "communication_lost", "message": "...", "axis": "j1" | null}
  "mcu": {
    "connected": true, "transport": "virtual",
    "target": "virtual", "board": "virtual", "firmware_version": "0.1.0",
    "protocol_version": 1, "build_hash": "51570001",
    "capabilities": ["stepper.step_dir", "endstop", "watchdog"],
    "queue_capacity": 32, "queue_depth": 0,
    "clock_offset_us": -1520, "rtt_us": 180,
    "tx_frames": 2201, "rx_frames": 2199, "crc_errors": 0,
    "last_heartbeat_ms_ago": 12           // or null
  },
  "axes": [{"id": "j1", "homed": true, "enabled": true, "commanded_position": 45.0,
            "measured_position": 44.98, "velocity": 0.0, "endstop": false}]
}
```

- Fault codes: `communication_lost`, `queue_underrun`, `segment_late`, `limit_hit`, `soft_limit`, `homing_failed`, `mcu_fault`, `plugin_failed`, `emergency_stop`. See [Faults](https://motioncore.bzy-logics.com/reference/errors#robot-faults).
- MCU string fields are `""` until the MCU has identified itself.
- Telemetry describes one MCU (v1 engines support one per robot). A future additive change will introduce `mcus: McuStatus[]`.
- `measured_position` comes from the MCU step counter; there is no encoder feedback in v1.

### RobotConfig

```jsonc
{
  "name": "single-axis-bench", "profile": "org.motioncore.robot.single_axis",
  "mcus": [{"name": "main", "board": "virtual", "transport": "virtual", "watchdog_ms": 500}],
  "axes": [{
    "id": "j1", "name": "Base", "kind": "revolute", "unit": "deg",
    "min": -170.0, "max": 170.0, "max_velocity": 90.0, "max_acceleration": 360.0,
    "home_position": -170.0,                // or null
    "steps_per_unit": 44.444, "mcu": "main", "mcu_channel": 0,
    "actuator": "org.motioncore.actuator.step_dir",
    "step_pin": "STEP0", "dir_pin": "DIR0", "enable_pin": "EN0",
    "endstop_pin": "ENDSTOP0",              // or null
    "geometry": {                           // or null; engine-api >= 1.1
      "parent": null,                       // parent axis id, null = robot base
      "origin_mm": [0.0, 0.0, 120.0],       // joint origin in the parent frame
      "rotation_deg": [0.0, 0.0, 0.0],      // fixed roll, pitch, yaw (about X, Y, Z, in that order)
      "axis": [0.0, 0.0, 1.0],              // unit rotation axis / slide direction
      "tip_mm": null                        // or [x, y, z]: end of this joint's link (e.g. tool flange)
    }
  }]
}
```

`step_pin`, `dir_pin` and `enable_pin` are always non-empty; `endstop_pin` is `null` when the axis has no endstop.

`geometry` places the joint in the robot's kinematic tree, following the URDF joint convention: `frame(child) = frame(parent) · Translate(origin_mm) · RPY(rotation_deg) · Joint(q about axis)`, where `frame(parent)` includes the parent's joint motion and `q` is in deg (revolute) or mm (prismatic). Either every axis has `geometry` or none does; it is `null` with engines older than API 1.1. It describes the machine for display only — commands still address one axis at a time. See [MotionCore: geometry](https://motioncore.bzy-logics.com/guide/configuration#_7-geometry-optional).

### Plugin

```jsonc
{"id": "org.motioncore.robot.single_axis", "version": "0.2.0", "api_version": "1",
 "kind": "builtin", "description": "...", "provides": ["robot.profile"],
 "requires": ["robot.joint_model"], "permissions": ["robot.read_geometry"],
 "state": "active", "error": null}
// state: discovered | validated | resolved | initialized | active | failed | deactivated
```

### FirmwareTarget

```json
{"board": "stm32f411-blackpill", "description": "...", "firmware_target": "stm32f4",
 "rust_target": "thumbv7em-none-eabihf", "step_channels": 1, "max_step_rate": 20000,
 "provisional": true, "is_virtual": false}
```

### FirmwareJob

```jsonc
{"id": "build-3", "kind": "build",          // build | flash
 "state": "succeeded",                      // queued | running | succeeded | failed
 "mcu": "main", "created_unix": 1794765721,
 "finished_unix": 1794765790,               // null while not finished
 "message": "built firmware.elf (build dcdfcb82)", "log": "...",
 "artifact": {"board": "stm32f411-blackpill", "firmware_version": "0.1.0", "build_hash": "dcdfcb82",
              "image_sha256": "7e61...", "toolchain": "rustc 1.99.1",
              "required_capabilities": ["endstop"]}   // or null
}
```
