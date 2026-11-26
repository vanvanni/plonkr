# Plonkr CLI (`plonk`) — v0.1.0-dev

The `plonkr/` repository root implements the initial v0.1.0 development CLI for **Plonkr** (`plonk`) in Go (`go 1.26.0` module floor).

## Package layout

- `main.go`: CLI entry point wiring signal cancellation and exit-code mapping.
- `cmd/`: Cobra commands (`run`, `validate`, `version`), dynamic profile flag discovery, and cross-platform deploy-key preflight (`ReadKeySource`).
- `profile/`: HCL v2 parser, `configuration` (`groups`, `users`, `timezone`), strict `setup`/`script` task phase separation, `include` chaining, snippet expansion, `os.Root`-confined unit template rendering, and default profile/log path resolution.
- `runner/`: Sequential execution engine, unified step/task/run status model (`pending`, `running`, `completed`, `failed`, `skipped`, `bypassed`, `planned`, `cancelled`), and OpenSSH subprocess transport.
- `provision/`: Target Linux POSIX shell generators for groups, users, timezone, direct deploy-key installation (`~/.ssh/plonkr-<name>.key`), repository checkout/verification, and systemd unit installation.
- `runlog/`: Private append-only run journal with streaming ANSI/control sanitization and durable summary output.
- `ui/`: Bubble Tea live terminal screen using the Pegs palette and unified status indicators.
- `examples/`: Validated sample profiles (`minimal.hcl`, `app-machine.hcl`), snippets, and systemd unit template.

## Usage

Build and run checks from the `plonkr/` repository root:

```sh
go build -o plonk .
go vet ./...
go test -count=1 ./...
```

Commands:

- `plonk version` — print the CLI build version.
- `plonk validate PROFILE [--profiles-dir DIR]` — parse, validate, and preview the expanded plan without SSH.
- `plonk run HOST PROFILE [flags]` — execute a profile over OpenSSH.
  - `--dry-run` — preview planned, skipped, and bypassed steps without SSH or reading deploy keys.
  - `--bypass NAME` — bypass a named task (repeatable, case-sensitive exact match; validated against the loaded plan).
  - `--profiles-dir DIR` — override the profiles base directory.
  - `--no-tui` — force plain streaming output instead of the Bubble Tea screen.
  - `--tty` — allocate a remote PTY for interactive prompts (disables TUI; managed actions still use noninteractive `sudo`/`runuser`).
  - Dynamic profile flags discovered from `flag("name", ...)` are registered automatically; inspect them with `plonk run HOST PROFILE --help`.

## Profile storage & deploy keys

- **Profiles directory**: `PLONK_PROFILES_DIR` when set (must be absolute); otherwise `%ProgramData%\plonkr\` on Windows and `/etc/plonkr/` on Unix.
- **Run logs**: `PLONK_LOG_DIR` when set (must be absolute); otherwise user cache (`plonkr\logs`) on Windows and `$XDG_STATE_HOME/plonkr/logs` (or `~/.local/state/plonkr/logs`) on Unix.
- **Repository SSH keys**: Direct local file path only (the PoC key store has been removed). Unencrypted deploy keys are validated locally without modifying file bytes and copied to `$HOME/.ssh/plonkr-<name>.key` (`0600`) with SHA-256 and conflict checks. Note: if `ssh` connects as a non-root user different from `repository.user`, stdin passes through `sudo -n -H -u`, so connecting directly as `root` or `repository.user` avoids target sudo input logging.

## Verification status

For the full opt-in suite on a Windows or Linux Multipass host, run from the repository root:

```sh
go test -tags multipass ./acceptance -v -count=1 -timeout 45m
```

It checks the website and native CLI, launches a disposable Ubuntu VM, runs the
Linux race suite, and tests examples and provisioning through real SSH with
generated fixture keys and a private local Git repository. It collects results
and deletes its own VM. Normal `go test ./...` does not launch Multipass.
See [suite usage, coverage and debugging](acceptance/README.md).

- Verified on Windows (`go1.27.0`, `CGO_ENABLED=0`): `go vet ./...`, `go test -count=1 ./...`, and `GOOS=linux GOARCH=amd64 go vet ./...` + Linux cross-build pass.
- Verified by the opt-in suite from Windows: Ubuntu 24.04 guest, Go 1.26.0, Linux vet and the full race suite, and 13 real-SSH acceptance scenario groups. The VM is deleted after results are collected.
- Pending: suite launch from a Linux development host, native Windows OpenSSH transport, and Windows symlink-escape test (requires symlink privilege). The Linux guest covers symlink confinement through the existing package tests. These checks do not constitute a release or broad platform certification.
