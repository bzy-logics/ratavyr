# Engine contract & codegen

ratavyr talks to the engine through gRPC bindings generated from the **engine API contract**, which is owned and versioned by motioncore: `crates/engine-api/proto/motioncore/engine/v1/engine.proto`, released as git tags `engine-api/v0.MINOR.PATCH`. See [motioncore: engine gRPC API](https://motioncore.bzy-logics.com/reference/engine-api) and [contracts & versioning](https://motioncore.bzy-logics.com/internals/versioning).

Compatibility: **engine-api 0.2.x** (`motioncore.engine.v1`). At every connect, ratavyr calls `GetInfo` and refuses an engine whose `api_version` is not `0.2.x`. While MotionCore is pre-1.0, patch releases are additive and minor releases may break clients, so ratavyr accepts only the `0.MINOR` it was built against (`SupportedAPIMinor` in `internal/engine/client.go`).

## No copy of the proto

This repository never contains the `.proto`. Instead, [`contract.lock`](https://github.com/bzy-logics/ratavyr/blob/main/contract.lock) pins exactly which file the bindings came from:

```ini
repo=github.com/bzy-logics/motioncore
tag=engine-api/v0.1.4
commit=7ad321b46a72b8d7f1960b4dcd26f3020ea797a8
path=crates/engine-api/proto/motioncore/engine/v1/engine.proto
import_path=motioncore/engine/v1/engine.proto
sha256=2272cecf793c29629925b7aefdc9420e8cf0314c38970dd9e58a765817ac0f02
```

The generated code in `gen/` is committed. Don't edit it by hand.

## Scripts

```sh
scripts/generate.sh          # regenerate gen/
scripts/check-contract.sh    # verify gen/ matches contract.lock (CI)
```

`generate.sh`:

1. reads the proto with `git show <tag>:<path>` from a local motioncore clone (`$MOTIONCORE_REPO`, default `../motioncore`);
2. checks the tag resolves to the recorded commit (warns if not) and that the file's SHA-256 matches (fails if not);
3. writes it to `.contract/` (gitignored);
4. runs `go tool buf generate` ([`buf.gen.yaml`](https://github.com/bzy-logics/ratavyr/blob/main/buf.gen.yaml)).

`buf`, `protoc-gen-go` and `protoc-gen-go-grpc` are pinned in `go.mod` with the `tool` directive and run with `go tool`; nothing needs installing.

`check-contract.sh` runs the same steps into a temporary directory and fails if the result differs from `gen/`.

## Moving to a newer contract

For example, to `engine-api/v0.1.5`:

1. Fetch the tag in your motioncore clone: `git -C ../motioncore fetch --tags`.
2. Update `tag`, `commit` and `sha256` in `contract.lock` together:
   ```sh
   git -C ../motioncore rev-parse 'engine-api/v0.1.5^{commit}'
   git -C ../motioncore show engine-api/v0.1.5:crates/engine-api/proto/motioncore/engine/v1/engine.proto | sha256sum
   ```
3. Run `scripts/generate.sh`.
4. Map any new fields in `internal/api/rest` and document them in `docs/rest-api.md`, the source of truth for the HTTP API.
5. Tell [sindrava](https://sindrava.bzy-logics.com/development/api-client) about REST changes: it mirrors the API in `src/types/api.ts`.

A new minor version (e.g. `0.2.0`) may break clients: review the changes and update `SupportedAPIMinor` in `internal/engine/client.go`.

The engine API was first numbered `1.x`; versions 1.0.0–1.2.0 are now 0.1.0–0.1.4. See [contracts & versioning](https://motioncore.bzy-logics.com/internals/versioning#engine-api).

## Mapping rules (proto → REST)

- Enum values lose their prefix and become lowercase: `ROBOT_STATE_EMERGENCY_STOPPED` → `"emergency_stopped"`.
- Values unknown to this build (from a newer engine with the same `0.MINOR`) become `"unknown"`; `*_UNSPECIFIED` becomes `"unspecified"`.
- Unset optional fields become `null`, never omitted.
- Empty strings that mean "none" in the proto (e.g. `endstop_pin`) become `null`.
