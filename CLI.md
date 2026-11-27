# Plonkr CLI (`plonk`) — v2026.11.0-beta.1

The `plonkr/` repository root implements the initial v2026.11.0 development CLI for **Plonkr** (`plonk`) in Go (`go 1.26.0` module floor).

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
  - `--auto-approve` — replace differing target deploy keys without an approval prompt. The warning is still printed. Without this flag, differing keys require `y`/`yes` in a terminal; declining or running unattended leaves the old key untouched. Runs using deploy keys use plain output so warnings and prompts remain visible.
  - `--tty` — allocate a remote PTY for interactive prompts (disables TUI; managed actions still use noninteractive `sudo`/`runuser`).
  - Dynamic profile flags discovered from `flag("name", ...)` are registered automatically; inspect them with `plonk run HOST PROFILE --help`.

## Profile storage & deploy keys

- **Profiles directory**: `PLONK_PROFILES_DIR` when set (must be absolute); otherwise `%ProgramData%\plonkr\` on Windows and `/etc/plonkr/` on Unix.
- **Run logs**: `PLONK_LOG_DIR` when set (must be absolute); otherwise user cache (`plonkr\logs`) on Windows and `$XDG_STATE_HOME/plonkr/logs` (or `~/.local/state/plonkr/logs`) on Unix.
- **Repository SSH keys**: Direct local file path only (the PoC key store has been removed). Unencrypted deploy keys are validated locally without modifying file bytes and copied to `$HOME/.ssh/plonkr-<name>.key` (`0600`) with SHA-256 and conflict checks. OpenSSH-format keys must use LF line endings and end with a newline; `plonk` rejects CRLF or newline-less key files locally with a clear message instead of letting the target fail with `error in libcrypto`. The file is never rewritten.
- **Checkout directory**: when the checkout path does not exist and the repository user cannot create it (for example `/srv/app` under a root-owned `/srv`), `plonk` creates the directory for that user first, provided the SSH login is root or has passwordless sudo. An empty existing directory is accepted as a clone target. A non-empty directory that is not the expected repository is still refused.
- **Profile files**: Windows (CRLF) line endings in profiles, snippets and unit templates are normalised to LF, so commands and unit files never carry a carriage return to the target.
- **Plain output**: `--no-tui` and piped runs print `[completed]`, `[failed]`, `[cancelled]`, `[skipped]`, `[bypassed]` and `[planned]` for each step, matching the run log.

Note: if `ssh` connects as a non-root user different from `repository.user`, stdin passes through `sudo -n -H -u`, so connecting directly as `root` or `repository.user` avoids target sudo input logging.

## Verification status

Keyed repositories get a managed block in the target user's `~/.ssh/config`:

```sshconfig
Host your-repo.github.com
  HostName github.com
  IdentityFile ~/.ssh/plonkr-your-repo.key
  IdentitiesOnly yes
```

The alias uses the repository name and the original SSH host. Explicit SSH URL
ports are retained. Git clones and later pulls use that alias; matching existing
checkouts are migrated to it. Repeated runs update the managed block without
duplicating it or deleting unrelated SSH settings. The key is set to `0600`
immediately when installed, reused or replaced. SSH config is also private (`0600`).

Repository Git connections automatically accept unknown or changed SSH server
keys and bypass host-key verification. Optional `known_hosts` entries remain valid
profile data, but are not used to verify these connections. This behavior also
applies to later pulls through the checkout's saved SSH command. Authentication
and repository permissions are still required. The controller-to-target SSH
connection retains the user's normal OpenSSH policy.

Profiles load their required `include` profiles, referenced snippets and unit
templates before any remote execution. A bare name such as `web` resolves from
`--profiles-dir`, then `PLONK_PROFILES_DIR`, then the platform default. An explicit
path such as `./web.hcl` loads that file. Without `--profiles-dir`, a bare
`web.hcl` prefers an existing file in the current directory before checking the
profile directory. With `--profiles-dir`, a bare `web.hcl` resolves in that
directory. Includes/snippets/templates resolve from the selected base, not from
arbitrary directories, and unrelated profiles are not executed.

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
- Verified by the opt-in suite from Windows: Ubuntu 24.04 guest, Go 1.26.0, Linux vet and the full race suite, and 13 real-SSH acceptance scenario groups. The VM is deleted after results are collected. Re-verified 2026-11-27 after the stabilization fixes (13/13 groups, Linux race suite).
- Pending: suite launch from a Linux development host, native Windows OpenSSH transport, and Windows symlink-escape test (requires symlink privilege). The Linux guest covers symlink confinement through the existing package tests. These checks do not constitute a release or broad platform certification.
