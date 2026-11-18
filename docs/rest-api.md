# ratavyr REST/WebSocket API — v1

Owned by `ratavyr`. Consumed by `sindrava` and other HTTP
clients. The server translates between this API and the engine's gRPC
contract (`motioncore.engine.v1`, generated bindings in `gen/`). It never
derives robot state; every robot field below is relayed from the engine.

## Conventions

* JSON, `snake_case` keys.
* Enums are lowercase strings without prefix (`ROBOT_STATE_EMERGENCY_STOPPED` → `"emergency_stopped"`).
* Optional values that are unset are `null` (never omitted).
* Units: axis display units (`deg` / `mm`).
* Errors: HTTP status + `{"error": {"code": "...", "message": "..."}}`.

| code | HTTP |
|---|---|
| `bad_request`, `validation_failed`, `not_homed` | 400 |
| `unauthorized` | 401 |
| `forbidden` | 403 |
| `not_found`, `unknown_axis`, `unknown_command` | 404 |
| `method_not_allowed` (with `Allow` header) | 405 |
| `invalid_state` | 409 |
| `collision` | 409 |
| `unsupported_media_type` | 415 |
| `rate_limited` (with `Retry-After`) | 429 |
| `mcu_error` | 502 |
| `engine_unavailable` | 503 |
| `engine_timeout` | 504 |
| `internal`, anything else | 500 |

## Authentication and authorization

Tokens are configured per role. Each request authenticates with
`Authorization: Bearer <token>` and gets the role of that token:

| role | token variable | may |
|---|---|---|
| `viewer` | `MOTIONCORE_VIEWER_TOKEN` | read everything, WebSocket, `stop`, `estop` |
| `operator` | `MOTIONCORE_OPERATOR_TOKEN` | viewer + all other commands, firmware builds |
| `admin` | `MOTIONCORE_API_TOKEN` | operator + firmware flashing |

* Every `/api/*` endpoint except `GET /api/health` and `GET /api/info` needs
  a token once any token is configured. A missing or wrong token gets 401
  `unauthorized`; an action above the caller's role gets 403 `forbidden`.
* Stopping is open to every authenticated role: anyone who can see the robot
  may stop it.
* After 10 failed authentications within a minute, a client IP gets 429
  `rate_limited` (even with a correct token) until the window passes.
* **No token configured** = authentication off: every request is treated as
  `admin`, and the server refuses to listen on anything but loopback.
  `MOTIONCORE_API_TOKEN` alone keeps the previous behavior (one token, full
  access).

### WebSocket authentication

Browsers cannot set headers on WebSocket requests. Get a short-lived ticket
with the normal bearer token and pass it in the URL:

```
POST /api/ws-tickets   {}            -> 201 {"ticket": "...", "expires_in_ms": 30000}
GET  /api/ws?ticket=<ticket>
```

A ticket is valid **once**, for 30 s, and carries the role of the token that
requested it. `?access_token=<token>` still works but is **deprecated**: it
puts the long-lived token into URLs and access logs. The server logs a
warning each time it is used. It will be removed in a future major API
version.

## Request rules

* **Cross-site protection.** `POST` requests may not use `text/plain`,
  `application/x-www-form-urlencoded` or `multipart/form-data` (415
  `unsupported_media_type`); send `application/json` or no body type. When a
  browser sends an `Origin`, it must be the server's own origin or match
  `MOTIONCORE_ALLOWED_ORIGINS` (403 `forbidden` otherwise).
* **DNS-rebinding protection.** While authentication is off, the `Host`
  header must be `localhost`, a loopback IP, or match
  `MOTIONCORE_ALLOWED_HOSTS` (403 `forbidden`).
* **Rate limit.** `POST` requests are limited per client IP
  (`MOTIONCORE_RATE_LIMIT` per second, burst `MOTIONCORE_RATE_BURST`;
  defaults 20/40). `stop` and `estop` are **never** limited.
* Request bodies are at most 64 KiB; unknown JSON fields are rejected.
  Axis ids are 1–64 characters of letters, digits, `_`, `-` and `.`.
* Every API response carries `Cache-Control: no-store`.

## Endpoints

| method | path | body | response |
|---|---|---|---|
| GET | `/api/health` | – | `{"status":"ok","engine":{"connected":bool}}` |
| GET | `/api/info` | – | `{"server_version":"0.3.0","auth_required":bool,"engine":EngineInfo\|null,"engine_error":string\|null}` |
| GET | `/api/session` | – | `Session` (the caller's role and permissions) |
| POST | `/api/ws-tickets` | `{}` | `{"ticket":string,"expires_in_ms":30000}` (201) |
| GET | `/api/status` | – | `Telemetry` |
| GET | `/api/config` | – | `RobotConfig` |
| GET | `/api/plugins` | – | `Plugin[]` |
| POST | `/api/commands/{name}` | params object (may be `{}`) | `{}` |
| GET | `/api/firmware/targets` | – | `FirmwareTarget[]` |
| GET | `/api/firmware/jobs` | – | `FirmwareJob[]` (newest first) |
| GET | `/api/firmware/jobs/{id}` | – | `FirmwareJob` |
| POST | `/api/firmware/builds` | `{"mcu": string?}` | `FirmwareJob` (202) |
| POST | `/api/firmware/flashes` | `{"build_job_id": string, "confirm": true}` | `FirmwareJob` (202) |
| GET | `/api/ws` | – | WebSocket, see below |

Command names and params:

| name | params | minimum role |
|---|---|---|
| `stop`, `estop` | none | viewer |
| `enable`, `disable`, `reset_fault` | none | operator |
| `jog` | `{"axis": "j1", "delta": 5.0, "velocity"?: 30.0}` | operator |
| `move_to` | `{"axis": "j1", "position": -90.0, "velocity"?: 30.0}` | operator |
| `home` | `{"axis": "j1"}` | operator |
| `home_all` | `{}` | operator |

A successful command response (`{}`) means the engine **accepted** the
command, not that it has finished. For `estop` in particular, clients must
wait for telemetry with `"state": "emergency_stopped"` before showing the
stop as confirmed. A software emergency stop never replaces a hardware
E-stop circuit.

Firmware: `POST /api/firmware/builds` needs operator, `POST
/api/firmware/flashes` needs admin.

## WebSocket `/api/ws`

Server → client messages only (commands go through REST):

```json
{"type": "engine", "data": {"connected": true}}
{"type": "telemetry", "data": Telemetry}
{"type": "log", "data": {"time_ms": 1234, "level": "warn", "message": "..."}}
```

On connect the client receives `engine`, then the latest cached telemetry if
any. Slow clients are disconnected (close code 1013).

## Types

```jsonc
// Session
{"auth_required": true, "role": "operator",            // viewer|operator|admin
 "permissions": {"view": true, "stop": true, "command": true,
                 "firmware_build": true, "firmware_flash": false}}
// Without configured tokens: {"auth_required": false, "role": "admin", all permissions true}
```

```jsonc
// EngineInfo
{"api_version": "0.1.4", "engine_version": "0.2.0", "mcu_protocol_version": 1,
 "plugin_api_version": "0", "fault_injection_enabled": false}

// Telemetry
{
  "seq": 1042, "time_ms": 52100,
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
            "measured_position": 44.98, "velocity": 0.0, "endstop": false,
            "homing": false}]        // homing: this axis is searching for its switch (engine-api >= 0.1.4)
}
// fault codes: communication_lost queue_underrun segment_late limit_hit soft_limit
//              homing_failed mcu_fault plugin_failed emergency_stop
// mcu string fields are "" until the MCU has identified itself.

// RobotConfig
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
    "geometry": {                           // or null (profile without geometry; engine-api < 1.1)
      "parent": null,                       // parent axis id, null = robot base
      "origin_mm": [0.0, 0.0, 120.0],       // joint origin in the parent frame
      "rotation_deg": [0.0, 0.0, 0.0],      // fixed roll, pitch, yaw (about X, Y, Z, in that order)
      "axis": [0.0, 0.0, 1.0],              // unit rotation axis / slide direction in the joint frame
      "tip_mm": [200.0, 0.0, 0.0],          // or null: end of this joint's link (e.g. tool flange), joint frame
      "radius_mm": 20.0                     // or null: link thickness of the collision model (engine-api >= 0.1.4)
    }
  }],
  "home_all": {                             // or null (engine-api < 1.2): what the home_all command does
    "groups": [["j1"]],                     // axes of a group home together; groups run in order
    "start_pose": null,                     // or the pose the plan was designed for; show it before homing
    "from_profile": false                   // false = default plan, one axis per group in profile order
  },
  "collision_checking": false               // true: once all axes are homed, colliding moves are refused (409 collision)
}

// Plugin
{"id": "org.motioncore.robot.single_axis", "version": "0.2.0", "api_version": "0",
 "kind": "builtin", "description": "...", "provides": ["robot.profile"],
 "requires": ["robot.joint_model"], "permissions": ["robot.read_geometry"],
 "state": "active", "error": null}        // state: discovered|validated|resolved|initialized|active|failed|deactivated

// FirmwareTarget
{"board": "stm32f411-blackpill", "description": "...", "firmware_target": "stm32f4",
 "rust_target": "thumbv7em-none-eabihf", "step_channels": 1, "max_step_rate": 20000,
 "provisional": true, "is_virtual": false}

// FirmwareJob
{"id": "build-3", "kind": "build", "state": "succeeded",   // kind: build|flash; state: queued|running|succeeded|failed
 "mcu": "main", "created_unix": 1794765721, "finished_unix": 1794765790,   // finished_unix: null while not finished
 "message": "built firmware.elf (build dcdfcb82)", "log": "...",
 "artifact": {"board": "stm32f411-blackpill", "firmware_version": "0.1.0", "build_hash": "dcdfcb82",
              "image_sha256": "7e61...", "toolchain": "rustc 1.99.1", "required_capabilities": ["endstop"]}  // or null
}
```

## Clarifications

* **Unknown enum values** (from a newer engine with the same `0.MINOR`) are reported as `"unknown"`;
  `*_UNSPECIFIED` as `"unspecified"`. Clients must tolerate both.
* **Firmware errors:** `POST /api/firmware/flashes` without `"confirm": true` →
  400 `bad_request` (checked before the engine is contacted); unknown job →
  404 `not_found`; a job that is not a succeeded build → 400 `bad_request`;
  drives energised or another job running → 409 `invalid_state`. Building
  for a virtual board → 400 `bad_request`.
* **`POST /api/firmware/builds`**: clients should always send `mcu`; omitting
  it only works when the robot has exactly one MCU.
* **401 responses** use the error envelope with code `unauthorized` and carry
  `WWW-Authenticate: Bearer`.
* **Geometry** follows the URDF joint convention:
  `frame(child) = frame(parent) · Translate(origin_mm) · RPY(rotation_deg) · Joint(q about axis)`,
  where `frame(parent)` includes the parent's joint motion (`q` in deg for
  revolute, mm for prismatic). Either every axis has `geometry` or none has.
  It describes the machine for display; it does not enable coordinated moves.
* **`engine_error`** in `/api/info` explains why the engine is not
  connected, e.g. `incompatible engine api_version "0.2.0" (this server
  requires 0.1.x)` or the last connection error; `null` while connected.
* **Firmware verification** (comparing the running MCU firmware with an
  artifact) is a CLI-only engine feature (`motioncore firmware verify`); the
  engine API has no RPC for it yet, so this server cannot offer it.
* **Telemetry describes one MCU** (v1 engines support one MCU per robot). A
  future additive change will introduce `mcus: McuStatus[]` keyed by name.
* Pin fields `step_pin`, `dir_pin`, `enable_pin` are always present
  (non-empty); `endstop_pin` is `null` when the axis has no endstop.
