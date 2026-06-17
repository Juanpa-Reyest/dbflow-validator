# dbflow-validator

`dbflow-validator` is a self-contained CLI that validates a PostgreSQL Maven DB archetype by cloning the repository, spinning up an ephemeral Postgres container, running `mvn dbflow:sync` and `mvn dbflow:rollback` inside a Maven container on a shared Docker network, and reporting `PASSED` or `FAILED` with a full per-step trace. The binary ships with a vendored Maven repository embedded inside it — **no Maven installation or JVM is required on the host machine**.

---

## Prerequisites

| Dependency | Required | Notes |
|------------|----------|-------|
| **Docker** | Yes | Daemon must be running (`docker info` must succeed) |
| **Git** | Yes | Must be on `PATH` |
| Maven / JVM | **No** | Runs inside the `maven:3.9-eclipse-temurin-21` container automatically |

---

## Install

### From source (Go 1.25+)

```bash
make build          # build for the current platform → dist/dbflow-validator
./install.sh        # copy to /usr/local/bin (or ~/.local/bin when not writable)
```

### Cross-compile all platforms

```bash
make build-all      # produces dist/dbflow-validator-{linux,darwin,windows}-{amd64,arm64}
```

The compiled binary is ~118 MB because the vendored Maven repository is embedded inside it. No loose asset files are needed alongside the binary.

### Windows

Copy `dist/dbflow-validator-windows-amd64.exe` to a directory that is on your `PATH`. See `install.sh` for the note about Windows.

---

## Usage

### Interactive (TTY)

When `--repo-url` and `DBFLOW_GIT_TOKEN` are not provided and stdin is a terminal, the tool prompts for them:

```
$ dbflow-validator validate
Repository URL: https://github.com/org/db-artifacts-myproject.git
Git access token (hidden):
```

The token is read with echo suppressed and is never written to disk, logs, or process arguments.

### With flags

```bash
DBFLOW_GIT_TOKEN=<token> dbflow-validator \
  --repo-url   https://github.com/org/db-artifacts-myproject.git \
  --base-branch integracion \
  --output-format console
```

### With JSON output

```bash
DBFLOW_GIT_TOKEN=<token> dbflow-validator \
  --repo-url    https://github.com/org/db-artifacts-myproject.git \
  --output-format json \
  --output-file  result.json
```

---

## Flag Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--repo-url` | — | Git repository URL to clone and validate (prompted interactively when absent and TTY) |
| `--base-branch` | `integracion` | Branch to validate |
| `--output-format` | `console` | Output format: `console` or `json` |
| `--output-file` | — | Write JSON output to this path (optional) |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `DBFLOW_GIT_TOKEN` | Git access token (alternative to interactive prompt; never logged) |

---

## Configuration

No configuration file is required. All inputs are flags or environment variables. The embedded Maven repo is extracted to `~/.cache/dbflow-validator/<version>/m2` on the first run; subsequent runs reuse the cached extraction without re-extracting.

---

## Output

### Console (default)

```
[PASSED] preflight        (12 ms)
[PASSED] clone            (3421 ms)
[PASSED] start-postgres   (8304 ms)
[PASSED] patch            (2 ms)
[PASSED] dbflow:sync      (26311 ms)
[PASSED] first-tag        (1 ms)
[PASSED] dbflow:rollback  (13266 ms)

Overall: PASSED
```

### JSON

```json
{
  "status": "PASSED",
  "steps": [
    { "name": "preflight",       "status": "PASSED", "durationMs": 12 },
    { "name": "clone",           "status": "PASSED", "durationMs": 3421 },
    { "name": "dbflow:sync",     "status": "PASSED", "durationMs": 26311 },
    { "name": "dbflow:rollback", "status": "PASSED", "durationMs": 13266 }
  ]
}
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Validation PASSED |
| `1` | Validation FAILED |
| `2` | Configuration or usage error |
| `130` | Aborted by SIGINT/SIGTERM |

---

## Troubleshooting

### Docker daemon is not running

```
dbflow-validator: preflight check failed: Docker is installed but the daemon is not running — start Docker and retry
```

Start the Docker daemon (`sudo systemctl start docker` on Linux, open Docker Desktop on macOS/Windows) and re-run.

### Maven image pull fails

```
start maven container: failed to pull image "maven:3.9-eclipse-temurin-21": ...
```

Check your internet connection or ensure the Docker daemon can reach the Docker Hub registry. Alternatively, pull the image manually: `docker pull maven:3.9-eclipse-temurin-21`.

### BUILD FAILURE in Maven output

The tool prints the full Maven stdout/stderr trace when a step fails. Look for `[ERROR]` lines in the trace — common causes:

- Missing or malformed changesets in `src/main/resources/db/`.
- A missing tag in `master-changelog.xml` (rollback requires a tag to exist).
- Network connectivity issue between the Maven container and the Postgres container (rare; usually a Docker network problem).

### Maven artifact resolution fails (offline)

If you see `Could not find artifact ...` errors in the trace, the embedded vendored repository may be incomplete. The embedded repo currently covers:

- `com.gs.ftt.coe-ds:relational-db-release-manager-plugin:0.0.1`
- `org.postgresql:postgresql:42.7.4`

If the plugin version changes, rebuild the binary from source after updating `internal/embedrepo/mvn-vendor/repository`.

### Maven/JVM not found — this is expected

`dbflow-validator` does **not** require `mvn` or `java` on the host. Maven and the JVM run inside the `maven:3.9-eclipse-temurin-21` Docker container. Preflight checks only `docker` and `git`.

### Cached extraction

The first run extracts the embedded Maven repo to `~/.cache/dbflow-validator/<version>/m2`. If you suspect a corrupt extraction, delete that directory and re-run — extraction will happen automatically.
