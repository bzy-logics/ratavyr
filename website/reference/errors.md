# Error codes

Every error is an HTTP status plus a JSON envelope:

```json
{"error": {"code": "not_homed", "message": "axis j1 must be homed before absolute moves"}}
```

Clients should branch on `code`, not on `message`.

| Code | HTTP | Meaning |
|---|---|---|
| `bad_request` | 400 | Malformed request; e.g. flash without `"confirm": true`, build for a virtual board, `home` on an axis without homing config, a job that is not a succeeded build |
| `validation_failed` | 400 | Motion rejected by the engine's planner: outside soft limits, velocity too high, non-finite value |
| `not_homed` | 400 | Absolute move on an unhomed axis |
| `unauthorized` | 401 | Missing or wrong token. Carries `WWW-Authenticate: Bearer` |
| `forbidden` | 403 | Role too low; or a cross-origin request, or a disallowed `Host` while auth is off |
| `not_found` | 404 | Unknown resource, e.g. firmware job |
| `unknown_axis` | 404 | Axis id not in the robot configuration |
| `unknown_command` | 404 | No such command name |
| `method_not_allowed` | 405 | With an `Allow` header |
| `invalid_state` | 409 | Not allowed in the current robot state (moving while not `ready`; flashing while drives are on or another job runs) |
| `collision` | 409 | The move would make the robot's links collide or reach the floor (engines with collision checking, all axes homed). The message names the links and the joint value |
| `unsupported_media_type` | 415 | `POST` with `text/plain`, a form, or multipart content type |
| `rate_limited` | 429 | Too many `POST`s, or too many failed logins. Carries `Retry-After` |
| `mcu_error` | 502 | The MCU rejected the request or the link failed |
| `engine_unavailable` | 503 | ratavyr is not connected to the engine. `/api/info` → `engine_error` says why |
| `engine_timeout` | 504 | The engine did not answer within 5 s |
| `internal` | 500 | Anything else |

## Mapping from the engine

The engine reports gRPC status codes with the precise code in the `motioncore-error-code` trailer. ratavyr passes that code through and picks the HTTP status from it. See [engine gRPC errors](https://motioncore.bzy-logics.com/reference/engine-api#errors).

## Robot faults are not errors

A fault — `communication_lost`, `limit_hit`, `emergency_stop` and so on — is robot state, reported in telemetry's `fault` field, not as an HTTP error. See [MotionCore: faults](https://motioncore.bzy-logics.com/reference/errors#robot-faults).
