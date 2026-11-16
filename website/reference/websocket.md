# WebSocket

`GET /api/ws` streams live state to clients. It is **server → client only**: commands go through [REST](/reference/rest-api#commands).

## Connecting

Get a single-use ticket with your bearer token, then connect with it:

```text
POST /api/ws-tickets   {}     → 201 {"ticket": "...", "expires_in_ms": 30000}
GET  /api/ws?ticket=<ticket>
```

A ticket is valid once, for 30 s, and carries the role of the token that requested it. Request a new one for every reconnect. With authentication off, no ticket is needed. See [Authentication & roles](/guide/authentication#websocket-tickets).

The connection is subject to the same Origin check as `POST` requests: a browser `Origin` must be same-origin or match `MOTIONCORE_ALLOWED_ORIGINS`.

```js
const { ticket } = await fetch('/api/ws-tickets', {
  method: 'POST',
  headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  body: '{}',
}).then((r) => r.json())

const ws = new WebSocket(`${location.origin.replace(/^http/, 'ws')}/api/ws?ticket=${ticket}`)
ws.onmessage = (e) => {
  const msg = JSON.parse(e.data)
  if (msg.type === 'telemetry') render(msg.data)
}
```

## Messages

```json
{"type": "engine",    "data": {"connected": true}}
{"type": "telemetry", "data": Telemetry}
{"type": "log",       "data": {"time_ms": 1234, "level": "warn", "message": "..."}}
```

| Type | When | Data |
|---|---|---|
| `engine` | On connect, and whenever the engine connection changes | `{"connected": bool}` |
| `telemetry` | On connect (latest cached frame, if any), then ~20 Hz | [`Telemetry`](/reference/rest-api#telemetry) |
| `log` | Engine log events | `time_ms`, `level` (`debug` / `info` / `warn` / `error`), `message` |

On connect, the client receives `engine` first, then the latest cached telemetry if there is one.

While the engine is down, clients get `{"type":"engine","data":{"connected":false}}` and no telemetry. When it comes back, ratavyr resumes the stream automatically.

## Detecting stale data

`telemetry.seq` increases monotonically per engine process; a gap means samples were dropped. If no telemetry arrives for a while (sindrava uses 1.5 s), treat the data as stale and disable motion controls.

## Slow clients

A client that can't keep up is disconnected with close code **1013** (try again later) rather than holding up telemetry for everyone else. Reconnect with a new ticket, using backoff with jitter.

## Deprecated: `?access_token=`

`GET /api/ws?access_token=<token>` still works, but it puts the long-lived token into URLs and access logs. The server logs a warning each time it's used. It will be removed in a future major API version.
