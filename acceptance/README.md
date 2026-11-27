# Opt-in Multipass acceptance suite

Run from the `plonkr/` repository root (where `go.mod` lives) on Windows (PowerShell) or Linux:

```sh
go test -tags multipass ./acceptance -v -count=1 -timeout 45m
```

This is the single slow check before a release or after significant changes.
It is excluded from ordinary `go test ./...` by the `multipass` build tag;
`-count=1` prevents a cached result from standing in for a fresh VM run.

Requirements: Go at the module's declared version or newer, Node.js 24 or newer,
and a working Multipass installation with permission to launch instances. The
default VM is Ubuntu 24.04 with 2 CPUs, 3 GiB RAM and 12 GiB disk. Network access
is needed for the Ubuntu image, Ubuntu packages, Go and Go modules. Repository
acceptance tests use a private Git fixture inside the VM, without GitHub accounts
or personal credentials.

The suite:

1. Checks the website, runs host vet/unit tests, builds the native CLI and validates
   every top-level shipped example.
2. Creates a unique `plonkr-suite-*` instance. It copies CLI source, tests and
   examples through an archive; it does not mount the workspace or copy your SSH
   directory, environment files, Git metadata or Syncthing metadata.
3. Installs the exact Go version declared in `go.mod`, verifying the archive against
   the official Go download SHA-256. Runs Linux vet, all package tests with the
   race detector, builds the CLI, then runs real SSH acceptance scenarios.
4. Collects logs/results and deletes only the instance created by this invocation.
   Existing instances are not stopped or deleted; no global purge is used.

## Debugging and alternate images

```sh
go test -tags multipass ./acceptance -v -count=1 -timeout 45m -args -keep
go test -tags multipass ./acceptance -v -count=1 -timeout 45m -args -image 24.04 -artifacts ./suite-results
```

`-artifacts` must name a new directory. By default the suite prints a newly created
OS temporary directory. `suite.log` records host and Multipass commands and output;
`guest-checks.log` records Linux checks; `acceptance-results.json` records guest
scenario results. Guest artifacts may be absent if launch/bootstrap fails.
Use a directory outside the checkout for local results.

Failures normally remove the VM too. `-keep` retains it, including its disposable
fixture credentials, and prints the exact cleanup command. To inspect it, use
`multipass shell <printed-instance-name>`. Remove it with
`multipass delete --purge <printed-instance-name>` when finished.

Ctrl+C requests cancellation and cleanup. Force-killing the test process, shutting
down the host, or the outer Go timeout can prevent deferred cleanup. The VM name
is printed before launch; inspect and remove that exact instance in such cases.
The internal 40-minute deadline leaves time before the documented outer timeout.

## Coverage and limits

Package tests cover parser validation, ordering, include/snippet expansion,
template confinement, environment paths, terminal rendering, cancellation and
error paths. The guest scenarios exercise actual CLI processes and OpenSSH against
the disposable VM, including managed accounts, repository authentication, units,
timezone and result reporting. The application example is copied into scratch
and its external repository/key/host-trust placeholders are replaced with fixture
values; the checked-in example stays unchanged.

The 14 guest scenario groups cover:

- All shipped example profiles: validation, preview and execution (external
  placeholders are replaced in the application example). Adding another example
  requires adding an execution scenario; validation alone does not satisfy the suite.
- Minimal example over SSH, plus `/etc/plonkr`, environment override and explicit
  directory precedence.
- Application setup: automatic users/groups, explicit GID, public login key,
  service-account defaults, timezone, rendered unit contents/ownership/mode.
- Deferred creation and explicit creation of users/groups.
- Repeat setup and a subsequent `git pull` as the application user.
- Branch, tag and full commit repository references.
- Exact private-key bytes, target ownership/mode, unchanged source, missing and
  encrypted-key preflight, dry-run isolation, declined/interactive/automatic key
  replacement, and automatic acceptance of unknown or changed repository host keys.
- Nested flags, configured/CLI bypass, dynamic help, unknown options, sanitized
  output, private logs and absence of private key material in logs.
- Task locks: `exists` and `eval` directives alone and combined, state recorded
  after the task, changed results rerunning, `--force`, dry-run, explicit bypass
  precedence, root-owned private state written through sudo by the non-root
  login, exists-only locks keeping no state, and a failed task recording nothing.
- Chained profiles/nested snippets, fail-fast exit codes and pending work.
- Invalid phase/include-cycle rejection before any remote action.
- Real PTY terminal UI, remote `--tty` allocation and cancellation after a remote
  readiness marker confirms execution has started.

The Linux CLI performs guest SSH acceptance. Host-native checks validate/build the
Windows or Linux controller, but this suite does not yet prove native Windows
OpenSSH transport, Windows ACL enforcement, interactive password authentication,
every Linux distribution, or network-failure recovery. The terminal cases are
automated PTY checks rather than visual or accessibility certification. Passing
this suite is broad regression evidence, not proof of every possible profile.

Multipass lifecycle references:
[launch](https://canonical.com/multipass/docs/latest/reference/command-line-interface/launch/)
and [remove a named instance](https://canonical.com/multipass/docs/latest/how-to-guides/manage-instances/remove-an-instance/).

## Verification record

2026-11-26: passed from Windows with Multipass 1.17.0, an Ubuntu 24.04 amd64
guest, and Go 1.26.0 inside the guest. Website/native checks, Linux vet/race tests
and all 13 guest scenario groups passed. Artifact collection and automatic VM
removal were verified. A run launched from a Linux host remains to be performed.

2026-11-27: re-verified after the canonical module-path update and SSH key-policy
changes. All 13 groups and Linux race checks passed, including actual PTY yes/no
approval, automatic key replacement and unknown/mismatched repository host keys.

2026-11-27: added the task-lock scenario group. All 14 groups and Linux race checks
passed from Windows on Ubuntu 24.04, with the repository SSH alias work
included in the archive. The VM was removed.
