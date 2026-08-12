# Contributing

Thanks for your interest in improving the `hyperlift` CLI.

## Prerequisites

- Go 1.26+
- `make`
- [`golangci-lint`](https://golangci-lint.run/) v2 (for `make lint`)

## Building and testing

```sh
make build      # build ./bin/hyperlift with version ldflags
make test       # go test -race -cover ./... (same flags as CI)
make lint       # golangci-lint run ./...
```

## The contract pin

The tests in `internal/testapi` validate the mock server against a pinned copy
of the published Spaceship API OpenAPI document
(`internal/testapi/testdata/spaceship-public-api.json`). When the upstream
contract changes, refresh the pin:

```sh
make refresh-spec   # re-extracts the spec from the published docs
```

That re-extracts the published document and pins its hyperlift subset
(CI runs the same refresh on every PR and fails when the pin drifts). Then
confirm the contract tests stay green: `go test ./internal/testapi/`.

## Running against the mock API

The repo ships a contract-accurate mock of the Hyperlift External API. Use two
terminals to run the CLI end-to-end without real credentials:

```sh
# terminal 1
make mock              # serves on :8080 (override MOCK_ADDR)

# terminal 2: point the CLI at it, then use it normally
make build
export HYPERLIFT_BASE_URL=http://localhost:8080
export HYPERLIFT_API_KEY=demo HYPERLIFT_API_SECRET=demo

./bin/hyperlift apps list
./bin/hyperlift apps restart app_a1b2c3 --wait
```

`make run-mock ARGS='apps list'` does the same in one line and sets the
variables only for that command, which avoids leaving the override in your
shell.

The mock seeds two application ids: `app_a1b2c3` and `app_d4e5f6`. Remember
that `HYPERLIFT_BASE_URL` outranks the config file, so a shell that still
exports it keeps talking to the mock.

## Test conventions

- **Command tests use `clienttest.Fake`** (set only the function fields the
  test needs) with the shared fixture in `internal/cmdutil/cmdutiltest`. Each
  command package declares a narrow interface of just the client methods it
  calls; keep that pattern for new commands.
- **Wire-level tests use the mock** (`internal/testapi`) behind
  `httptest.NewServer` — never share one server between tests; the mock is
  stateful.
- **New wire shape? Hand-write the fixture** in `internal/client/testdata/`
  from the published documentation's examples. Never `json.Marshal` a Go
  struct into a fixture: the fixture must be able to disagree with the types,
  or the test proves nothing.
- **New endpoint in the spec?** `TestContractCoverage` fails until you add a
  scenario to `internal/testapi/contract_validation_test.go`.
- **Assert errors by type** (`errors.As(err, &apiErr)`, then `.Status` /
  `.Code`), not by message text.

## Pull requests

- Keep each change focused. A small PR gets a faster review.
- `go test ./...` must pass. New behavior needs tests.
- Code must be `gofmt`-clean. It must also pass `make lint`.
- Do not add a dependency before you discuss it in an issue.
