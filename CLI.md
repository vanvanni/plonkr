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
  - `--force` — run tasks even when their `lock` would bypass them; their lock state is still recorded afterwards. An explicit bypass still wins.
  - `--profiles-dir DIR` — override the profiles base directory.
  - `--no-tui` — force plain streaming output instead of the Bubble Tea screen.
  - `--auto-approve` — replace differing target deploy keys without an approval prompt. The warning is still printed. Without this flag, differing keys require `y`/`yes` in a terminal; declining or running unattended leaves the old key untouched. Runs using deploy keys use plain output so warnings and prompts remain visible.
  - `--tty` — allocate a remote PTY for interactive prompts (disables TUI; managed actions still use noninteractive `sudo`/`runuser`).
  - Dynamic profile flags discovered from `flag("name", ...)` are registered automatically; inspect them with `plonk run HOST PROFILE --help`.

## Task locks

A task may carry an optional `lock` block with `exists` (absolute target paths) and/or `eval` (commands run on the target over SSH as the login user). The task is bypassed only when every directive set holds, and the run continues with the next task. It is then reported as `bypassed` with the reason `bypassed by lock: …`.

```hcl
task "Install Go" {
  order  = 10
  script = ["./install-go.sh"]
  lock {
    exists = ["/usr/local/go/bin/go"]
    eval   = ["/usr/local/go/bin/go version"]
  }
}
```

- `exists` holds when every path exists. It stores nothing and needs no privileges.
- `eval` holds when each command's result (a SHA-256 over exit status and stdout; stderr is ignored) equals the result recorded after the task last completed. No record means the task runs. A failing command is a result, not an error.
- State is recorded after the task succeeds, by running the `eval` commands again, so it reflects the machine the task produced. A failed task records nothing. A bypassed task (profile, `--bypass`, or lock) records nothing, and a task whose steps are all disabled by flags is neither probed nor recorded.
- State lives on the target in `/etc/plonkr/.states/<id>.json` (directory `0700`, files `0600`, root-owned), one file per profile name, written atomically as root or through passwordless `sudo -n`. `<id>` is the first 16 hex digits of the SHA-256 of the profile name; the file records the name and, per task name, the lock-definition digest, the result hashes and a timestamp. Editing a `lock` block changes its digest and so invalidates the saved result.
- Locked task names must be unique across the expanded plan because they key the state. A run per target and profile at a time is assumed; concurrent runs may overwrite each other's state.
- `--dry-run` never connects, so planned steps only note that the lock is evaluated when the task runs.
- A probe or state-write failure fails the task and stops the run, like any other step failure.

## Profile storage & deploy keys

- **Profiles directory**: `PLONK_PROFILES_DIR` when set (must be absolute); otherwise `%ProgramData%\plonkr\` on Windows and `/etc/plonkr/` on Unix.
- **Run logs**: `PLONK_LOG_DIR` when set (must be absolute); otherwise user cache (`plonkr\logs`) on Windows and `$XDG_STATE_HOME/plonkr/logs` (or `~/.local/state/plonkr/logs`) on Unix.
- **Repository SSH keys**: Direct local file path only (the PoC key store has been removed). Unencrypted deploy keys are validated locally without modifying file bytes and copied to `$HOME/.ssh/plonkr-<name>.key` (`0600`) with SHA-256 and conflict checks. OpenSSH-format keys must use LF line endings and end with a newline; `plonk` rejects CRLF or newline-less key files locally with a clear message instead of letting the target fail with `error in libcrypto`. The file is never rewritten.
- **Checkout directory**: when the checkout path does not exist and the repository user cannot create it (for example `/srv/app` under a root-owned `/srv`), `plonk` creates the directory for that user first, provided the SSH login is root or has passwordless sudo. An empty existing directory is accepted as a clone target. A non-empty directory that is not the expected repository is still refused.
- **Profile files**: Windows (CRLF) line endings in profiles, snippets and unit templates are normalised to LF, so commands and unit files never carry a carriage return to the target.
- **Plain output**: `--no-tui` and piped runs print `[completed]`, `[failed]`, `[cancelled]`, `[skipped]`, `[bypassed]` and `[planned]` for each step, matching the run log.

Note: if `ssh` connects as a non-root user different from `repository.user`, stdin passes through `sudo -n -H -u`, so connecting directly as `root` or `repository.user` avoids target sudo input logging.

## Decided behavior and edge cases

These behaviors are decided, documented here and pinned by tests (`profile/edge_test.go`, `cmd/e2e_test.go`, `provision/edge_test.go` and the property test in `runner/property_test.go`).

- **Includes merge once.** A profile reached by several include paths (a shared base included by two parents) is merged where it is first reached, depth first in listed order, and counts once against the step limit. Listing the same profile twice in one `include` list is a validation error. Locked tasks in a shared profile are therefore valid.
- **Reserved flag names.** `flag("NAME", ...)` rejects the built-in option names `help`, `tty`, `dry-run`, `no-tui`, `profiles-dir`, `bypass`, `version`, `auto-approve` and `force` when the profile loads. A test compares this list with the options `plonk run --help` shows, so a new built-in option cannot be added without reserving it.
- **`key` is for SSH URLs only.** A repository with an `https://` URL and a `key` is rejected at load time instead of copying a private key that is never used.
- **Task names.** Names need not be unique, except that locked tasks must be (state is keyed by name). `--bypass NAME` bypasses every task with that exact, case-sensitive name.
- **Lock results.** An `eval` result is the exit status plus standard output with trailing newlines removed; standard error is ignored. Each command runs in its own shell with standard input closed. A state file without a `version` field is read as the current version; a corrupt one fails the run and is never overwritten.
- **Untrusted text.** Remote output, task names, profile names and error messages have terminal control sequences and invalid UTF-8 removed before they reach the screen, the run log or the exit message.
- **Exit codes.** `0` success; the remote command's own status when a step fails (`255` when SSH itself cannot connect); `130` after cancellation; `1` for every validation, argument or local error. Validation, key and log-directory errors happen before any SSH connection.
- **Interrupted SSH config update.** The managed `~/.ssh/config` block is updated under a lock directory, `~/.ssh/.plonkr-config.lock`. A run killed at that moment can leave it behind. The next run refuses to continue and prints the path to remove; Plonkr never removes a lock it does not own.

## Running the edge-case suite

```sh
go test -count=1 ./...                                    # unit, property and end-to-end tests
go test ./profile -run '^$' -fuzz FuzzParse -fuzztime 60s # explore beyond the seed inputs
```

Fuzz targets: `FuzzParse`, `FuzzParseWithIncludes`, `FuzzValidRepositoryURL`, `FuzzValidRepositoryRef`, `FuzzValidKeyPath`, `FuzzValidKnownHostLine`, `FuzzValidPublicKeyLine` (`profile`), `FuzzQuote` (`provision`), `FuzzCleanerNeverEmitsControlCharacters` (`runlog`) and `FuzzMessageText` (`ui`). Their seeds run as ordinary tests; a failing input is saved under `testdata/fuzz/` beside the package and replays on every later `go test`. `cmd/e2e_test.go` runs the real CLI against a fake `ssh` executable, so exit codes, cancellation and "no connection was made" guarantees are checked without a VM. The shell scripts that run on the target are executed in a real POSIX shell with stubbed system tools.

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
