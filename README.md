<div align="center">
  <img src=".github/hyperlift_logo.png" alt="Hyperlift" width="140" />

# hyperlift

[![test](https://github.com/NCCloud/hyperlift-cli/actions/workflows/test.yaml/badge.svg)](https://github.com/NCCloud/hyperlift-cli/actions/workflows/test.yaml)
[![lint](https://github.com/NCCloud/hyperlift-cli/actions/workflows/lint.yaml/badge.svg)](https://github.com/NCCloud/hyperlift-cli/actions/workflows/lint.yaml)
[![release](https://img.shields.io/github/v/release/NCCloud/hyperlift-cli?sort=semver)](https://github.com/NCCloud/hyperlift-cli/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/nccloud/hyperlift-cli)](https://goreportcard.com/report/github.com/nccloud/hyperlift-cli)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/NCCloud/hyperlift-cli)](go.mod)

</div>

> `hyperlift` is the command-line interface for
> [Hyperlift](https://www.spaceship.com/starlight-cloud/hyperlift/)
> applications on the Spaceship platform. It talks to the [Spaceship External
> API](https://docs.spaceship.dev/), and authenticates with an API key and secret.

- **Lifecycle**: inspect, build, start, stop and restart applications
- **Environment**: read and write environment variables
- **Logs**: runtime and build logs, with `--follow`
- **Metrics**: time series in each series' own unit
- **Scripting**: `--json` and `--quiet` output with documented shapes
- **Self-update**: checksum-verified, in place

Every command carries its own reference: run `hyperlift <command> --help`.

## 📦 Installation

### Install script (Linux & macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/nccloud/hyperlift-cli/main/scripts/install.sh | sh
```

The script detects your OS/architecture, downloads the matching release archive,
verifies it against the release's `SHA256SUMS`, and installs `hyperlift` into
`/usr/local/bin`. Override the target with `HYPERLIFT_INSTALL` or pin a version
with `HYPERLIFT_VERSION`:

```sh
HYPERLIFT_VERSION=v1.2.3 HYPERLIFT_INSTALL="$HOME/.local/bin" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/nccloud/hyperlift-cli/main/scripts/install.sh)"
```

### Windows

Download `hyperlift_windows_amd64.zip` from the
[Releases page](https://github.com/nccloud/hyperlift-cli/releases), extract it,
and put `hyperlift.exe` in a folder on your `PATH`.

You only do this once. `hyperlift update` replaces the binary in place after
that.

### Manual download

Take the archive for your platform from the
[Releases page](https://github.com/nccloud/hyperlift-cli/releases), extract it,
and move the `hyperlift` binary onto your `PATH`.

### From source

```sh
git clone https://github.com/nccloud/hyperlift-cli
cd hyperlift-cli
make install   # installs into $(go env GOPATH)/bin
```

Requires Go 1.25+.

### Verifying a download (optional)

Every release ships a `SHA256SUMS` file and a cosign signature over it. The
install script checks both for you. To check a manual download yourself:

```sh
sha256sum -c SHA256SUMS --ignore-missing   # Linux
shasum -a 256 -c SHA256SUMS --ignore-missing   # macOS
```

On Windows, use `Get-FileHash -Algorithm SHA256 <file>` and compare the value
with the matching line in `SHA256SUMS`.

The checksum proves the file arrived intact. The signature goes further and
proves this repository's release workflow produced it. Checking the signature
needs [cosign](https://github.com/sigstore/cosign):

```sh
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp 'https://github.com/nccloud/hyperlift-cli/.github/workflows/release.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

### Upgrading

```sh
hyperlift update          # download, verify, and replace this binary
hyperlift update --check  # report whether a newer release exists
```

The CLI also tells you once a day when a newer release is available. See
[`update`](#version-and-update) for the details.

## 🚀 Quick start

```sh
hyperlift auth login                    # paste your key, then your secret
hyperlift apps list                     # your applications
hyperlift apps get <app-id>             # one application in detail
hyperlift logs <app-id> --follow        # stream its log lines
```

## 🔑 Authentication

`hyperlift` authenticates with a Spaceship API key and secret, sent as the
`X-API-Key` and `X-API-Secret` headers. Create a pair in the
[API Manager](https://www.spaceship.com/application/api-manager/) and give it
the scopes your work needs:

| Scope                | Grants                                                       |
| -------------------- | ------------------------------------------------------------ |
| `hyperlift:read`     | `apps list`, `apps get`, `logs`, `metrics`                    |
| `hyperlift:execute`  | `apps build`, `apps start`, `apps stop`, `apps restart`       |
| `hyperlift:manage`   | `env get`, `env set`, `env unset`                             |

A read-only key is enough to inspect applications and read their logs and
metrics. Add `hyperlift:execute` to build or restart them, and
`hyperlift:manage` for environment variables, which need it even to read.
The secret appears once, at creation.

`hyperlift auth login` then stores them: the key goes in the config file
(`~/.config/hyperlift/config.yml`), and the secret goes in your operating
system's keyring. Passing `--base-url` stores that too.

On a machine with no keyring, `login` says so and writes the secret to a
`0600` file next to the config instead.

A secret passed with `--secret` becomes a command-line argument, so it lands in
your shell history and is visible to `ps` while the command runs. Prefer the
prompt, or pipe the secret on stdin:

```sh
# Interactive (prompts for key, then secret; secret input is hidden)
hyperlift auth login

# Non-interactive, with the secret as an argument
hyperlift auth login --key "$KEY" --secret "$SECRET"

# Pipe the secret on stdin so it never becomes a command-line argument
printf '%s' "$SECRET" | hyperlift auth login --key "$KEY" --with-stdin
```

Check or clear your session:

```sh
hyperlift auth whoami    # show base URL, masked key, and login state
hyperlift auth logout    # remove stored credentials
```

If a command reports `Not logged in`, run `hyperlift auth login`. A `403` means
your key is missing a required scope; the CLI names that scope.

### Configuration & environment

The config file honors `$XDG_CONFIG_HOME`. It holds the API key, the base URL,
and an optional `default_output`. It never holds the secret.

| Variable                    | Purpose                                            |
| --------------------------- | -------------------------------------------------- |
| `HYPERLIFT_BASE_URL`        | Override the API base URL (e.g. point at a mock).  |
| `HYPERLIFT_API_KEY`         | Override the API key without writing it to config. |
| `HYPERLIFT_API_SECRET`      | Override the API secret stored in the OS keyring.  |
| `HYPERLIFT_NO_UPDATE_CHECK` | Disable the background update check (any value).   |
| `NO_COLOR`                  | Disable colored output.                            |
| `XDG_CONFIG_HOME`           | Override the config directory location.            |

Precedence for the base URL and API key is: environment variable > config file >
built-in default (`https://spaceship.dev/api/v1`). For the API secret it is:
environment variable > OS keyring.

## 🏳️ Global flags

These flags work on every command:

| Flag      | Description                                                 |
| --------- | ----------------------------------------------------------- |
| `--json`  | Emit JSON instead of a table.                               |
| `--quiet` | Emit only the primary identifier, one per line.             |
| `--debug` | Print a redacted one-line trace per HTTP request to stderr. |

`hyperlift --version` prints the version. It works on the root command only;
`hyperlift version` is the subcommand that does the same and more.

`--json` and `--quiet` cannot be combined. [Machine output](#machine-output)
lists what each command emits under them.

## 💻 Commands

Every command has `--help` with its full flag list. This section covers the
behavior you would not guess from a flag name.

### `apps`: application lifecycle

```sh
hyperlift apps list [--take N] [--skip N] [--all]
hyperlift apps get <app-id>
hyperlift apps build <app-id>
hyperlift apps start <app-id>
hyperlift apps stop <app-id>
hyperlift apps restart <app-id>
```

`build`, `start`, `stop` and `restart` return as soon as the API accepts the
request. Add `--wait` to poll until the operation finishes: `build --wait`
watches `buildStatus` for `built` or `failed`, the others watch `status` for
`running` or `stopped`. `--timeout` bounds the wait and defaults to 10 minutes.

```sh
hyperlift apps build app_123 --wait --timeout 15m
```

### `env`: environment variables

```sh
hyperlift env get <app-id>
hyperlift env set <app-id> KEY=VALUE [KEY=VALUE...]     # KEY= sets an empty value
hyperlift env unset <app-id> KEY [KEY...]
```

The API replaces the whole map on write, so `set` and `unset` read the current
variables, apply your change, and write everything back.

The server renames variables as it stores them: upper-case, with dashes and
spaces turned into underscores. `APPLICATION_PORT` (default `8080`) is the port
Hyperlift connects to your application on.

A successful update restarts the application, and the server rejects further
env updates until it is running again. Retry after a moment.

### `logs`: runtime and build logs

```sh
hyperlift logs <app-id> [--follow] [--build] [--no-timestamps]
```

`--follow` polls every few seconds until the log finishes, which means the
build completed or the application stopped. The API has no streaming endpoint,
so this is polling rather than a live connection.

### `metrics`: time-series metrics

```sh
hyperlift metrics <app-id> [--since 1h] [--interval 5m] [--metrics <names>]
```

Available metrics: `memoryUsageBytes`, `cpuUsagePercentage`,
`networkReceiveRateBytes`, `networkTransmitRateBytes`,
`ephemeralStorageUsedMebibytes`, `persistentStorageUsedMebibytes`.

The table renders each series in its own unit and puts the plan quota in the
column header when the plan defines one. `--json` keeps the raw values.

### `auth`: credentials

```sh
hyperlift auth login [--key KEY] [--secret SECRET | --with-stdin] [--base-url URL]
hyperlift auth whoami      # alias: status
hyperlift auth logout [--yes]
```

### `version` and `update`

```sh
hyperlift version
hyperlift update           # download, verify, and replace this binary
hyperlift update --check   # report only
```

`update` verifies the download against the release's `SHA256SUMS`, fetched over
TLS, then replaces the running binary and re-execs it. It does not check the
cosign signature, which only `install.sh` does, and it refuses to replace a
binary a package manager owns. Under `--json` or `--quiet` it reports without
installing.

The CLI also shows a daily notice when a newer release exists. It stays quiet
under `--json` and `--quiet`, on non-TTYs, and in CI. `HYPERLIFT_NO_UPDATE_CHECK`
turns the check off entirely.

### Shell completion

To try completion in the current bash or zsh shell:

```sh
source <(hyperlift completion bash)   # or: zsh
```

fish and PowerShell need different syntax. `hyperlift completion <shell> --help`
prints the exact line for your shell, and where to put it to make it permanent.

## 📤 Output formats

By default commands print human-readable tables. Use `--json` for scripting.
Use `--quiet` to print only the primary identifiers; for `env`, the keys. You
can set a default output format in the config file with `default_output`.

```sh
hyperlift apps get app_123 --json | jq '.status'
for key in $(hyperlift env get app_123 --quiet); do echo "$key"; done
```

### Machine output

All JSON the CLI emits uses camelCase keys. `--quiet` prints one primary
identifier per line; where a command has none, it prints nothing. Per command:

| Command                                     | `--json` top level                                          | `--quiet` line                       |
| ------------------------------------------- | ----------------------------------------------------------- | ------------------------------------ |
| `apps list`                                 | `{"items": [<application>], "total": <n>}`                  | application id                       |
| `apps get`                                  | `<application>`                                             | application id                       |
| `apps build` / `start` / `stop` / `restart` | `<application>` (full object, with or without `--wait`)     | application id                       |
| `env get` / `set` / `unset`                 | `{"KEY": "VALUE", ...}` (the resulting map)                 | variable key                         |
| `logs`                                      | NDJSON: one `{"message", "timestamp"}` object per line      | (no effect; output is already lines) |
| `metrics`                                   | `{"metrics": [<series>]}` (the raw payload)                 | series name                          |
| `auth login` / `logout`                     | `{"loggedIn": <bool>}`                                      | (nothing)                            |
| `auth whoami`                               | `{"baseUrl", "apiKey", "source", "loggedIn"}`               | masked API key                       |
| `version`                                   | `{"version", "commit", "date", "goVersion", "platform"}`    | version                              |
| `update`                                    | `{"currentVersion", "latestVersion", "updateAvailable"}`    | new version; nothing when current    |

`update` under `--json` or `--quiet` is check-only and never installs. `auth
whoami --json` answers the question even for invalid or missing credentials:
it emits `{"loggedIn": false}` and exits with code 2.

## 🚦 Exit codes

| Code | Meaning                                                               |
| ---- | --------------------------------------------------------------------- |
| 0    | Success.                                                              |
| 1    | Error (API error, invalid arguments, declined confirmation, ...).     |
| 2    | Authentication or authorization failure (any `401` or `403`).         |
| 130  | Interrupted (Ctrl-C / SIGTERM).                                       |

API error messages show the machine error code (for example
`(business.validationFailed)`) only when `--debug` is set.

## 🛠️ Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for build, test, and lint instructions,
and for the bundled contract-accurate mock API that the CLI can run against.

### Releasing

To cut a release, publish a GitHub Release. The workflow runs GoReleaser, which
builds the cross-platform archives, writes `SHA256SUMS`, and keyless-signs it
with cosign.

## 📄 License

Licensed under the [Apache License 2.0](LICENSE).
