# claude-status

A small CLI that checks the [Anthropic / Claude status page](https://status.claude.com) from your terminal.

It fetches the public Statuspage API and prints the overall status, per-component health, any active incidents or scheduled maintenance, and (when everything is quiet) the most recently resolved incident.

## Usage

```
claude-status [flags]
```

Example output:

```
✓ Claude Status — All Systems Operational
updated 2m ago · https://status.claude.com

Components
  ● claude.ai                             Operational
  ● Claude Console (platform.claude.com)  Operational
  ● Claude API (api.anthropic.com)        Operational
  ● Claude Code                           Operational

Most Recent Resolved Incident
  ● Elevated errors on Claude 3.5 Sonnet  RESOLVED
    resolved 3d ago
    ...
```

### Flags

| Flag         | Description                                                    |
| ------------ | -------------------------------------------------------------- |
| `--json`     | Output the raw JSON summary from status.claude.com              |
| `--no-color` | Disable ANSI colors (auto-disabled when stdout isn't a TTY)     |
| `-h, --help` | Show help                                                       |

### Exit codes

Useful for scripting and monitoring:

| Code | Meaning                                              |
| ---- | ---------------------------------------------------- |
| `0`  | All systems operational                              |
| `1`  | One or more components degraded, or an active incident |
| `2`  | Unable to reach the status page                      |

## Install

Requires Go 1.26+.

```sh
go build -o claude-status .
```

Or install directly onto your `$PATH`:

```sh
go install .
```

## Testing

Run the test suite locally:

```sh
go test ./...
```

Or with the race detector and a coverage report, as CI runs it:

```sh
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on every push to `main` and on pull requests: `gofmt` check, `go vet`, build, and the test command above. The coverage profile is uploaded as a build artifact. Tests use local `httptest` servers — no network access is required.

## Environment variables

None. The tool requires no environment variables, API keys, or configuration files — the status API is public, and all behavior is controlled via the command-line flags above.

## Notes

- No dependencies outside the Go standard library.
- Network requests time out after 5 seconds.
