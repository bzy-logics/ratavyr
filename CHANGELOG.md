# Changelog

All notable changes to ratavyr, the MotionCore gateway. Validation levels follow MotionCore's: *implemented*, *simulated* (against the engine's virtual MCU), *software-tested* (automated tests in CI), *hardware-validated*. ratavyr itself drives no hardware; what it relays is validated as described in [MotionCore's changelog](https://github.com/bzy-logics/motioncore/blob/main/CHANGELOG.md).

## [0.3.0] — 2026-11-19

The first ratavyr release, part of the MotionCore v0.3.0 baseline.

### Compatibility

| Component | Version |
|---|---|
| MotionCore engine | v0.3.0; engine API pinned to `engine-api/v0.3.0` (`contract.lock`); engines with another `0.MINOR` are refused at the handshake |
| REST and WebSocket API | v1 ([`docs/rest-api.md`](docs/rest-api.md)) |
| sindrava | v0.3.0 |

### API — software-tested (unit, contract and integration tests against the engine)

- REST API: health, info, session, configuration, status, plugins, firmware targets, builds, flashes and jobs.
- Commands relayed to the engine: enable, disable, home, `home_all` (with the profile's home-all plan), jog, move, `move_j`, `move_j_path` (up to 256 waypoints), `move_l`, stop, `estop`, reset fault. Engine refusals, including collisions, are passed on as structured errors.
- Axis geometry and tool pose relayed from the engine.
- WebSocket stream of live telemetry and logs; the latest telemetry frame is cached for new clients.

### Security — software-tested

- Roles viewer < operator < admin on every route; viewers may `stop` and `estop`.
- Single-use, 30-second WebSocket tickets, so tokens never appear in URLs.
- Cross-site and DNS-rebinding protection; refuses to start unauthenticated on a non-loopback address.
- Brute-force blocking and per-IP rate limits, with `stop` and `estop` exempt.
- Audit log of every command, firmware build and flash, ticket and rejected authentication.

### Known limitations

- A software E-stop is relayed like any other command; it never replaces a hardware E-stop circuit.
- Pause and resume, their role policy and recovery policies are not implemented yet (#17).
- No command execution tracking (#9) or server-side alarms (#11).

[0.3.0]: https://github.com/bzy-logics/ratavyr/releases/tag/v0.3.0
