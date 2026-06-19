# dbflow-validator

Validate your database changes **on your own machine**, before opening a PR.

You point it at your archetype repository; it clones it, spins up a throwaway
PostgreSQL, runs `sync` and `rollback`, and tells you **PASSED** or **FAILED**
with a full trace. No changes are pushed anywhere. When it finishes, everything
it created is cleaned up automatically.

---

## Download

Go to the [Releases page](https://github.com/Juanpa-Reyest/dbflow-validator/releases) and download the binary for your OS:

| OS | File |
|----|------|
| Linux (x86-64) | `dbflow-validator-linux-amd64` |
| macOS Apple Silicon (M1+) | `dbflow-validator-darwin-arm64` |
| macOS Intel | `dbflow-validator-darwin-amd64` |
| Windows (x86-64) | `dbflow-validator-windows-amd64.exe` |

Optionally verify the download against `SHA256SUMS.txt` (also attached to each release), then follow the per-OS run steps below.

---

## What you need (only two things)

| You need | Why |
|----------|-----|
| **Docker**, running | The tool creates throwaway containers for the database and the build |
| **Git** | To clone your archetype repository |

You do **not** need Maven or Java installed — they run inside a container, automatically.

> Don't have Docker? Install **Docker Desktop** (Windows/macOS) or **Docker Engine** (Linux),
> start it, and make sure `docker` works in a terminal before continuing.

---

## Quickstart for developers

It's **one file**. Download the one for your system, run it, answer the prompt. That's it.

### 🪟 Windows

1. Download **`dbflow-validator-windows-amd64.exe`**.
2. Open a terminal (PowerShell) where you saved it and run it:
   ```powershell
   .\dbflow-validator-windows-amd64.exe
   ```
   (If Windows shows "Windows protected your PC" → **More info → Run anyway**. It's unsigned, not unsafe.)
3. It asks for your repository URL and access token. Done.

### 🍎 macOS

1. Download **`dbflow-validator-darwin-arm64`** (Apple Silicon / M1+) or **`dbflow-validator-darwin-amd64`** (Intel).
2. In a terminal, allow and run it:
   ```bash
   chmod +x dbflow-validator-darwin-arm64
   xattr -d com.apple.quarantine dbflow-validator-darwin-arm64   # macOS blocks unsigned downloads; this unblocks it
   ./dbflow-validator-darwin-arm64
   ```
3. It asks for your repository URL and access token. Done.

### 🐧 Linux

1. Download **`dbflow-validator-linux-amd64`**.
2. In a terminal:
   ```bash
   chmod +x dbflow-validator-linux-amd64
   ./dbflow-validator-linux-amd64
   ```
3. It asks for your repository URL and access token. Done.

> **Tip:** rename the file to `dbflow-validator` and move it somewhere on your `PATH`
> so you can just type `dbflow-validator` from anywhere. Optional — it works fine without that.

---

## What it asks you

When you run it with no options, it prompts for the repository URL.
If the URL is HTTPS it also prompts for an access token:

```
Repository URL:        https://github.com/your-org/your-archetype.git
Git access token (hidden):
```

The token is typed hidden and is **never** written to disk, logs, or anywhere — it's only used to clone.

> **SSH keys?** If you use an SSH URL (`git@github.com:your-org/your-archetype.git`),
> the tool clones using your existing SSH agent/keys — **no access token needed**.
> SSH URLs skip the token prompt entirely.

---

## What you'll see

The console is intentionally quiet — you see only high-level progress:

```
══════════════════════════════════════════════════════════════════
   ██████╗ ██████╗ ███████╗██╗      ██████╗ ██╗    ██╗
   ██╔══██╗██╔══██╗██╔════╝██║     ██╔═══██╗██║    ██║
   ██║  ██║██████╔╝█████╗  ██║     ██║   ██║██║ █╗ ██║
   ██║  ██║██╔══██╗██╔══╝  ██║     ██║   ██║██║███╗██║
   ██████╔╝██████╔╝██║     ███████╗╚██████╔╝╚███╔███╔╝
   ╚═════╝ ╚═════╝ ╚═╝     ╚══════╝ ╚═════╝  ╚══╝╚══╝
        V · A · L · I · D · A · T · O · R   v0.1
──────────────────────────────────────────────────────────────────
   Local database-change validation · fail fast before the PR
   zero side-effects
   ✒  Juanpa Reyest · Development Engineer
      ╭───────────╮
      │ ▸ ~/ _     │
      ╰───────────╯
══════════════════════════════════════════════════════════════════

  ✔ preflight                    OK (16ms)
  ✔ dbflow:sync                  OK (26s)
  ✔ dbflow:rollback              OK (13s)

  RESULT  ✔  PASSED          total 39s

  Detalles completos → dbflow-validator-runs/2026-06-18_19-45-06/execution.log
```

On **FAILED** runs the output shows `✘` and the failing step's error message:

```
  ✘ dbflow:rollback              FAILED (13s)

  RESULT  ✘  FAILED          total 1m 12s

  Detalles completos → dbflow-validator-runs/2026-06-18_19-45-06/execution.log
```

- **PASSED** → your changes apply and roll back cleanly. Good to open your PR.
- **FAILED** → the console shows the status; the **full trace is in `execution.log`** (see below).

---

## Other ways to run it (optional)

You don't have to use the prompt. You can pass everything as options:

```bash
# HTTPS: provide the token via environment variable + the repo via a flag
DBFLOW_GIT_TOKEN=<your-token> dbflow-validator \
  --repo-url https://github.com/your-org/your-archetype.git \
  --base-branch integration

# SSH: no token needed — uses your existing SSH keys automatically
dbflow-validator \
  --repo-url git@github.com:your-org/your-archetype.git \
  --base-branch integration

# Get machine-readable JSON instead of the console view
DBFLOW_GIT_TOKEN=<your-token> dbflow-validator \
  --repo-url https://github.com/your-org/your-archetype.git \
  --output-format json \
  --output-file result.json
```

Run `dbflow-validator --help` for the full list any time.

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--repo-url` | — | Repository to clone and validate (asked interactively if omitted) |
| `--base-branch` | `integration` | Branch to validate |
| `--sql-input` | `./src/main/resources/SQLInput` | Path to local SQLInput directory |
| `--output-format` | `console` | `console` or `json` |
| `--output-file` | — | Write the JSON result to this path |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--output-dir` | `./dbflow-validator-runs` | Directory for per-run artifact subdirectories |
| `--keep-workspace` | `false` | Retain the ephemeral clone under `<run>/workspace/` even on a PASSED run |
| `--version`, `-v` | — | Print the version and exit |
| `--help`, `-h` | — | Print help and exit |

| Environment variable | Description |
|----------------------|-------------|
| `DBFLOW_GIT_TOKEN` | Git access token for HTTPS URLs (instead of the interactive prompt; never logged). Not needed for SSH URLs. |

### Run artifacts

Every run creates a timestamped subdirectory under `--output-dir` (default `./dbflow-validator-runs`):

```
dbflow-validator-runs/
  2026-06-18_19-45-06/    ← timestamp: YYYY-MM-DD_HH-MM-SS (local time, human-readable)
    execution.log         ← full structured report: banner + step table + block traces
    report.json           ← machine-readable validation result (for IDE/CI integration)
    workspace/            ← ephemeral clone retained here on FAILED runs
```

**`execution.log`** is the full enterprise output document, structured top-to-bottom as:
1. The banner (version + tagline + signature)
2. `RUN <run-id>  ·  branch: <branch>  ·  schema: <schema>` header
3. Step summary table with ✔/✘ glyphs, step number, name, duration; failing steps show the error on an indented `└─` line
4. `RESULT  ✔/✘ STATUS   total <dur>` line
5. `DETALLE DE EJECUCIÓN` section — each step's full captured trace in a framed block:
   ```
   ┌─[ STEP 07 ]── DBFLOW:SYNC ────────────────────── ✔ 26s ─┐
   │  [INFO] BUILD SUCCESS
   │  [INFO] Sync complete
   └─────────────────────────────────────────────────────────┘
   ```

**`report.json`** is always written to the run dir (regardless of `--output-format`). It is machine-readable (same JSON schema as `--output-file` output) and is intended for IDE/CI integration and post-mortem scripting — NOT for human consumption; use `execution.log` for that.

**`workspace/`** contains the full ephemeral clone (your archetype + injected SQL files). It is:
- Retained on **FAILED** runs so you can inspect the generated changelog XML under
  `workspace/src/main/resources/db/schema/changelog/` and the patched `liquibase.properties`.
- Retained on **any** run when `--keep-workspace` is set.
- Removed on **PASSED** runs (unless `--keep-workspace` is set) — nothing to debug.

The git token and container credentials never appear in any persisted file.

**Disk usage note:** each run retains the full clone (~tens of MB). Prune periodically:

```bash
rm -rf dbflow-validator-runs/
```

`dbflow-validator-runs/` and the local binary `/dbflow-validator` are listed in `.gitignore` so they are never accidentally committed.

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Validation **PASSED** |
| `1` | Validation **FAILED** |
| `2` | Wrong usage / missing input |
| `130` | Cancelled with Ctrl-C |

---

## Troubleshooting

**"Docker is installed but the daemon is not running"**
Start Docker (open Docker Desktop on Windows/macOS, or `sudo systemctl start docker` on Linux) and run again.

**macOS: "cannot be opened because Apple cannot check it for malicious software"**
The binary is unsigned. Run `xattr -d com.apple.quarantine ./dbflow-validator-darwin-arm64` once (as shown in the macOS steps), or right-click the file → **Open** → **Open**.

**Windows: "Windows protected your PC"**
Click **More info → Run anyway**. The binary is unsigned, not malicious.

**"failed to pull image maven:3.9-eclipse-temurin-21"**
Your Docker can't reach the internet to download the build image the first time. Check your connection, or pre-pull it: `docker pull maven:3.9-eclipse-temurin-21`.

**FAILED with a Maven trace**
That's the tool doing its job — your changes have a problem. Look for `[ERROR]` lines in the printed trace. Common causes: a malformed changeset, or a missing tag in `master-changelog.xml`.

**First run is slow**
The first run downloads the build image and extracts the embedded Maven repository to `~/.cache/dbflow-validator/`. Later runs reuse the cache and are much faster.

---

## For maintainers (building & distributing)

> Developers don't need this section — it's for whoever builds and ships the binaries.
> Full publishing instructions (GitHub Packages, cutting releases, fresh-clone setup) are in [`docs/PUBLISHING.md`](docs/PUBLISHING.md).

Requires **Go 1.25+**. The binary is ~118 MB because the vendored Maven repository
(the `dbflow` plugin + PostgreSQL driver) is embedded inside it, so the distributed
file is fully self-contained.

The plugin jar (`relational-db-release-manager-plugin-0.0.1.jar`) is NOT committed to git.
It is fetched from GitHub Packages at build time. On a fresh clone, run:

```bash
export GH_TOKEN=ghp_your_token_with_read_packages
make vendor       # downloads the jar (no-op if already present)
```

Then build:

```bash
make build        # build for THIS machine            → dist/dbflow-validator
make build-all    # cross-compile all four platforms  → dist/dbflow-validator-<os>-<arch>
```

`make build-all` produces:

- `dist/dbflow-validator-linux-amd64`
- `dist/dbflow-validator-darwin-amd64` (macOS Intel)
- `dist/dbflow-validator-darwin-arm64` (macOS Apple Silicon)
- `dist/dbflow-validator-windows-amd64.exe`

**To distribute:** share those binaries with developers (a GitHub Release is the
ideal home — one download link per OS). Each file is the complete tool; nothing
else ships alongside it.

`install.sh` is a convenience for **macOS/Linux maintainers** to copy the right
binary onto their own `PATH` — it is not the developer install path (it's bash-only
and doesn't cover Windows).

**Updating the embedded plugin:** if the `dbflow` plugin version changes, replace
the jars under `internal/embedrepo/mvn-vendor/repository/` and rebuild.
