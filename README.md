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
  ● claude.ai            Operational
  ● platform.claude.ai   Operational
  ● api.anthropic.com    Operational

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

## Notes

- No dependencies outside the Go standard library.
- Network requests time out after 5 seconds.
