# Authentication & roles

## Roles

Tokens are configured per role. Each request authenticates with `Authorization: Bearer <token>` and gets that token's role.

| Role | Token variable | May |
|---|---|---|
| `viewer` | `MOTIONCORE_VIEWER_TOKEN` | Read everything, open the WebSocket, `stop`, `estop` |
| `operator` | `MOTIONCORE_OPERATOR_TOKEN` | Viewer + all other commands (`enable`, `disable`, `reset_fault`, `jog`, `move_to`, `home`), firmware builds |
| `admin` | `MOTIONCORE_API_TOKEN` | Operator + firmware flashing |

**Stopping is open to every authenticated role**: anyone who can see the robot may stop it. `stop` and `estop` are also never rate-limited.

## Rules

- Once any token is configured, every `/api/*` endpoint except `GET /api/health` and `GET /api/info` needs one.
- Missing or wrong token → `401 unauthorized`, with `WWW-Authenticate: Bearer`.
- An action above the caller's role → `403 forbidden`.
- After **10 failed authentications within a minute**, a client IP gets `429 rate_limited` — even with a correct token — until the window passes.
- Tokens must differ from each other. Tokens shorter than 16 characters are accepted with a warning; use long random ones:
  ```sh
  openssl rand -hex 32
  ```
- Setting only `MOTIONCORE_API_TOKEN` gives one full-access token, the pre-roles behaviour.
- **No token configured** = authentication off: every request is treated as `admin`, and the server refuses to listen on anything but loopback.

Roles are per token. There are no user accounts, and tokens can only be rotated by restarting.

## Discovering your role

`GET /api/session` tells a client its role and permissions, so it can hide controls it can't use. The server enforces the rules regardless.

```json
{"auth_required": true, "role": "operator",
 "permissions": {"view": true, "stop": true, "command": true,
                 "firmware_build": true, "firmware_flash": false}}
```

Without configured tokens: `{"auth_required": false, "role": "admin", ...}` with every permission true.

`GET /api/info` (public) reports `auth_required`, so a client knows whether to ask for a token before trying.

## WebSocket tickets

Browsers cannot set headers on WebSocket requests, and putting a long-lived token in a URL leaks it into logs. Instead, exchange the token for a ticket:

```text
POST /api/ws-tickets   {}     → 201 {"ticket": "...", "expires_in_ms": 30000}
GET  /api/ws?ticket=<ticket>
```

- A ticket is valid **once**, for **30 s**.
- It carries the role of the token that requested it.
- Request a fresh ticket for every connect and reconnect.

`?access_token=<token>` still works but is **deprecated**. The server logs a warning each time it's used, and it will be removed in a future major API version.

## Audit

Every command (with its parameters), firmware build and flash, WebSocket ticket and rejected authentication is recorded with role, client IP, outcome and duration. See [Deployment: audit log](/guide/deployment#audit-log).
