<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/design/assets/identity/plonkr-lockup-4x.svg" />
    <source media="(prefers-color-scheme: light)" srcset="docs/design/assets/identity/plonkr-lockup-4x-on-light.svg" />
    <img src="docs/design/assets/identity/plonkr-lockup-4x-on-light.svg" alt="Plonkr" width="420" height="128" />
  </picture>
</p>

<p align="center">
  <strong>Make every new machine yours.</strong><br />
  Configure existing machines over SSH, with a little less ceremony.
</p>

<p align="center">
  <a href="https://github.com/vanvanni/plonkr/actions/workflows/checks.yml?query=branch%3Aprerelease"><img src="https://github.com/vanvanni/plonkr/actions/workflows/checks.yml/badge.svg?branch=prerelease" alt="Checks on prerelease" /></a>
  <a href="https://github.com/vanvanni/plonkr/releases"><img src="https://img.shields.io/badge/status-beta-B5A0FF?style=flat&amp;labelColor=17181F" alt="Status: beta" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26%2B-8A74E0?style=flat&amp;labelColor=17181F&amp;logo=go&amp;logoColor=white" alt="Go 1.26 or newer" /></a>
  <a href="BRANCHES.md"><img src="https://img.shields.io/badge/versioning-CalVer-B5A0FF?style=flat&amp;labelColor=17181F" alt="Versioning: CalVer" /></a>
</p>

<p align="center">
  <a href="https://plonkr.dev">Website</a> ·
  <a href="CLI.md">CLI guide</a> ·
  <a href="examples/">Examples</a> ·
  <a href="https://github.com/vanvanni/plonkr/issues">Report an issue</a>
</p>

# Plonkr

Plonkr automates the setup work that comes after creating a machine. Describe users, groups, repositories and ordered tasks in a profile, then run it against a host you can already reach over SSH. The command is **plonk**.

**Beta — no stable release yet.** The current version is **2026.11.0-beta.4**. Install it with the scripts below or build from source. Interfaces and behavior may change before stable.

## What you can do

- Create users and groups automatically, or defer creation to an explicit task.
- Combine managed setup operations with ordered shell scripts and reusable snippets.
- Collect and validate profile arguments before connecting, then forward them explicitly to snippets.
- Clone repositories as the intended user using an existing local deploy-key file, copied byte-for-byte.
- Render and install systemd units and configure the target timezone.
- Preview a plan before connecting, enable profile-defined flags, or bypass selected tasks.
- Add a `lock` to a task so it is bypassed when a path already exists or a command's result is unchanged since the last completed run (`--force` overrides).
- Follow completed, failed, skipped and bypassed work in terminal output and saved logs.
- Pause on the controller or wait for SSH and remote readiness commands with bounded, cancellable waits.

Plonkr runs through your OpenSSH client and requires no agent on the target. Managed operations target Linux; systemd operations require systemd. Windows and Linux controller builds are available from source. Real SSH acceptance has been exercised with a Linux controller in a disposable Ubuntu 24.04 VM; native Windows SSH transport remains unverified.

## Install

Linux (amd64, arm64):

```sh
curl -fsSL https://plonkr.dev/install.sh | sh
```

Windows (amd64), in PowerShell:

```powershell
irm https://plonkr.dev/install.ps1 | iex
```

The scripts install the newest GitHub release, prereleases included, after checking its SHA-256 checksum, and change nothing else. Linux installs to `/usr/local/bin` as root and `~/.local/bin` otherwise; Windows installs for the current user and adds the folder to your user `PATH`. Pass `--version`, `--dir` or `--uninstall` to `install.sh` (after `sh -s --`), or set `PLONK_VERSION`, `PLONK_INSTALL_DIR` or `PLONK_UNINSTALL` before running `install.ps1`. Their sources are [docs/install.sh](docs/install.sh) and [docs/install.ps1](docs/install.ps1). They were tested against a local copy of the release layout, not yet against a published release.

## Build from source

Requires **Go 1.26+** and **OpenSSH**. Target account and system changes require root or the appropriate noninteractive sudo permissions.

```sh
git clone --branch prerelease https://github.com/vanvanni/plonkr.git
cd plonkr
go build -o plonk .
```

On Windows, use `go build -o plonk.exe .`. Run `./plonk` on Linux or `.\plonk.exe` in PowerShell; the commands below use `go run .` so they work from the repository root on either platform.

## Try a profile

Validate the included minimal example, then preview it without connecting:

```sh
go run . validate ./examples/minimal.hcl
go run . run my-host ./examples/minimal.hcl --dry-run
```

The example sets the timezone to UTC and runs two inspection commands:

```hcl
name = "Minimal machine check"

configuration {
  timezone = "UTC"
}

tasks {
  task "Inspect host" {
    order = 10
    script = ["uname -a", "id"]
  }
}
```

When ready to apply it, replace `my-host` with your SSH alias or `user@host`:

```sh
go run . run my-host ./examples/minimal.hcl
```

For accounts, repository access and unit templates, see [the application example](examples/app-machine.hcl). Replace its sample repository, key and host values before running it. The [CLI guide](CLI.md) explains prerequisites and execution behavior.

For typed inputs and validation, see [the arguments example](examples/arguments.hcl)
and [argument syntax](CLI.md#arguments). Supply values with repeatable
`--arg name=value` options, or answer the profile's questions in a terminal before
execution starts.

## Profiles and repository keys

Named profiles live in `/etc/plonkr/` on Linux or `%ProgramData%\plonkr\` on Windows. Set `PLONK_PROFILES_DIR` or pass `--profiles-dir` to use another directory. Includes, snippets and templates load with the selected profile. Every profile key is listed in the [profile reference](https://plonkr.dev/guide/reference.html).

Repository keys reference an absolute file path on your controller. Plonkr preserves that file's bytes and installs a private copy directly as `~/.ssh/plonkr-REPOSITORY.key` for the target user. A different existing key triggers a warning and an approval prompt. `--auto-approve` allows unattended replacement; identical keys do not prompt. The beta requires unencrypted keys.

Repository Git connections automatically accept unknown or changed SSH host keys, without verifying server identity against `known_hosts`. The controller-to-target connection follows your existing OpenSSH policy.

Each keyed repository gets its own SSH alias, such as `your-repo.github.com`, in
the target user's `~/.ssh/config`. The alias selects its deploy key; Git uses it
for cloning and subsequent pulls. Existing unrelated SSH configuration is preserved.

## Execution behavior

Work runs sequentially and stops at the first failure. There is no automatic rollback. Shell state does not carry between separate script items. Matching existing checkouts are verified; they are not automatically updated. Logs contain commands and output, so avoid printing secrets from profiles.

## Development and testing

Run from the repository root:

```sh
go vet ./...
go test -count=1 ./...
```

For the optional full suite, install Multipass and Node.js 24+, then run:

```sh
go test -tags multipass ./acceptance -v -count=1 -timeout 45m
```

The suite runs native checks, website validation, Linux race tests and real SSH scenarios in a disposable Ubuntu VM. It uses generated fixture credentials, saves results and removes its own VM. It is excluded from ordinary Go tests. See [coverage and debugging](acceptance/README.md).

The website and public docs live in `docs/`. Use `npm run check` to validate them and `npm run build` to generate `dist/`. [Pages setup](PAGES.md) covers the custom domain.

## Releases

Plonkr uses CalVer: `YYYY.MM.PATCH`, with candidate versions such as `2026.11.0-beta.4`. Development happens on `develop`, candidates on `prerelease`, and stable releases on `main`. See [branches and release rules](BRANCHES.md).

## License

Plonkr is free software, licensed under the [GNU General Public License v3.0](LICENSE) (`GPL-3.0-only`).
You may use, study, change and share it; if you distribute a modified version, it must be under the same license with its source available.
