# dbflow-validator

Validate your database changes **on your own machine**, before opening a PR.

You point it at your archetype repository; it clones it, spins up a throwaway
PostgreSQL, runs `sync` and `rollback`, and tells you **PASSED** or **FAILED**
with a full trace. No changes are pushed anywhere. When it finishes, everything
it created is cleaned up automatically.

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

- **PASSED** → your changes apply and roll back cleanly. Good to open your PR.
- **FAILED** → the full Maven trace is printed so you can see exactly what broke.

---

## Other ways to run it (optional)

You don't have to use the prompt. You can pass everything as options:

```bash
# HTTPS: provide the token via environment variable + the repo via a flag
DBFLOW_GIT_TOKEN=<your-token> dbflow-validator \
  --repo-url https://github.com/your-org/your-archetype.git \
  --base-branch integracion

# SSH: no token needed — uses your existing SSH keys automatically
dbflow-validator \
  --repo-url git@github.com:your-org/your-archetype.git \
  --base-branch integracion

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
| `--base-branch` | `integracion` | Branch to validate |
| `--output-format` | `console` | `console` or `json` |
| `--output-file` | — | Write the JSON result to this path |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--version`, `-v` | — | Print the version and exit |
| `--help`, `-h` | — | Print help and exit |

| Environment variable | Description |
|----------------------|-------------|
| `DBFLOW_GIT_TOKEN` | Git access token for HTTPS URLs (instead of the interactive prompt; never logged). Not needed for SSH URLs. |

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

Requires **Go 1.25+**. The binary is ~118 MB because the vendored Maven repository
(the `dbflow` plugin + PostgreSQL driver) is embedded inside it, so the distributed
file is fully self-contained.

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
