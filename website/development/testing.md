# Testing

## Unit and API tests

```sh
gofmt -l .                    # must print nothing
go vet ./...
go test ./...                 # unit + API tests against an in-process fake engine
go test -race ./...           # needs cgo (CI runs it on Linux)
```

API tests run against `internal/engine/enginetest`, an in-process fake gRPC engine, so they need no Rust toolchain.

## Integration tests

These run against the **real** engine with its virtual MCU, covering enable, home, jog, move, stop, E-stop, fault injection and reset, engine loss and recovery, the firmware API, and roles:

```sh
(cd ../motioncore && cargo build -p motioncore-cli)
MOTIONCORE_BIN=../motioncore/target/debug/motioncore MOTIONCORE_REPO=../motioncore \
  go test -tags integration -v ./internal/integration/
```

They live behind the `integration` build tag, so `go test ./...` skips them.

## Contract check

```sh
scripts/check-contract.sh
```

Verifies that `gen/` matches the engine contract pinned in `contract.lock`. See [Engine contract & codegen](/development/engine-contract).

## CI

[`.github/workflows/ci.yml`](https://github.com/bzy-logics/ratavyr/blob/main/.github/workflows/ci.yml) runs formatting, vet, race tests, the contract check (against a checkout of `bzy-logics/motioncore` with tags) and the integration tests (against a freshly built engine).

[`.github/workflows/docs.yml`](https://github.com/bzy-logics/ratavyr/blob/main/.github/workflows/docs.yml) builds this documentation site and deploys it to GitHub Pages.
