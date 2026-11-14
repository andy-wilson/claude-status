# claude-status

A small CLI that checks the [Anthropic / Claude status page](https://status.claude.com) from your terminal.

Getting the API error from within Claude Code is annoying. Having to go look at the claude status page in a browser more so. Hence, a basic CLI tool to go fetch the status. Yes there are more generic tools to fetch status for apps/companies who use Atlassian Statuspage, but I wanted something of basic which did *one thing*™.

It fetches the public Statuspage API and prints the overall status, per-component health, any active incidents or scheduled maintenance, and (when everything is quiet) the most recently resolved incident.

## Usage

```
claude-status [flags]
```

Example output:

```
✓ Claude Status - All Systems Operational
updated 2m ago · https://status.claude.com

Components
  ● claude.ai                                          Operational
  ● Claude Platform on AWS                             Operational
  ● Claude Console (platform.claude.com)               Operational
  ● Claude in Microsoft Foundry                        Operational
  ● Claude API (api.anthropic.com)                     Operational
  ● Claude Models on Google Cloud (Anthropic managed)  Operational
  ● Claude Code                                        Operational
  ● Claude Cowork                                      Operational
  ● Claude for Government                              Operational
  ● Claude on Cloud Platforms                          Operational

Most Recent Resolved Incident
  ● Elevated errors on Fable 52 and Mythos 52  RESOLVED
    resolved 3d ago
    latest: Overloaded errors on Fable 5.2 and Mythos 5.2 for four minutes. Incident has been mitigated.
    https://stspg.io/pytn9v8p2zc7
```

```
⚠ Claude Status — Partial System Outage
updated 5m ago · https://status.claude.com

Components
  ● claude.ai                                          Major Outage
  ● Claude Platform on AWS                             Operational
  ● Claude Console (platform.claude.com)               Operational
  ● Claude in Microsoft Foundry                        Operational
  ● Claude API (api.anthropic.com)                     Operational
  ● Claude Models on Google Cloud (Anthropic managed)  Operational
  ● Claude Code                                        Operational
  ● Claude Cowork                                      Degraded Performance
  ● Claude for Government                              Operational
  ● Claude on Cloud Platforms                          Operational

Active Incidents
  ⚠ Elevated errors on claude.ai, Claude Code, and platform.claude.com  MONITORING
    started 1h14m ago
    latest: A fix has been deployed for the issue affecting Claude services and we are monitoring for recovery.
    https://stspg.io/6kqyd0c5b1gr
  ⚠ Elevated errors affecting the Microsoft 365 connector  IDENTIFIED
    started 17h20m ago
    latest: This issue is caused by a Microsoft 365 service incident, and mail, calendar and search tools in the Microsoft 365 connector are still failing for most requests. Claude for Outlook remains affected. You can follow Mic...
    https://stspg.io/m0rbx38nmy1c
```

### Flags

| Flag         | Description                                                    |
| ------------ | -------------------------------------------------------------- |
| `--json`     | Output the raw JSON summary from status.claude.com              |
| `--no-color` | Disable ANSI colors (auto-disabled on stdout or stderr when that stream isn't a TTY) |
| `-h, --help` | Show help                                                       |

### Exit codes

Useful for scripting and monitoring:

| Code | Meaning                                              |
| ---- | ---------------------------------------------------- |
| `0`  | All systems operational                              |
| `1`  | One or more components degraded, or an active incident |
| `2`  | Unable to reach the status page, or its response was not recognised |
| `3`  | Invalid command-line usage                           |

The same exit codes apply with `--json`.

## Install

Requires Go 1.26.2+.

```sh
go build -o claude-status .
```

Or install directly onto your `$PATH`:

```sh
go install .
```

To cross-compile for Linux and macOS on both x86-64 and ARM64, run `make dist`. The binaries are written to `dist/` as `claude-status-<os>-<arch>`. Override `PLATFORMS` to build other targets:

```sh
make dist                             # linux and darwin, amd64 and arm64
make dist PLATFORMS=linux/arm64       # a single target
make dist PLATFORMS="windows/amd64"   # any GOOS/GOARCH pair Go supports
```

## Testing

Run the test suite locally:

```sh
go test ./...
```

Coverage targets are defined in the [`Makefile`](Makefile):

```sh
make cover        # run tests with -race, write coverage.out, print per-function coverage
make cover-html   # same, plus an HTML report in coverage.html
make cover-check  # same, and fail if total coverage is below the threshold (70%)
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs only on manual dispatch (Actions tab or `gh workflow run ci.yml`): `gofmt` check, `go vet`, build, and `make cover-check`, so a drop below the coverage threshold fails the build. The per-function coverage table is published to the job summary and `coverage.out` is uploaded as a build artifact. Tests use local `httptest` servers, so no network access is required.

## Environment variables

None. The tool requires no environment variables, API keys, or configuration files: the status API is public, and all behavior is controlled via the command-line flags above.

## Notes

- No dependencies outside the Go standard library.
- Network requests time out after 5 seconds.
