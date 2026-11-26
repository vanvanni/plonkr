# Plonkr

Machine setup, with a little less ceremony.

Plonkr is being built to automate repeat setup on existing LXC containers, VMs, and bare metal. The command is `plonk`. The repository root is the Go module for the v2026.11.0-beta.1 CLI. Disposable Ubuntu acceptance tests pass; release packaging remains pending, so there is no installable Plonkr release yet.

The [public guide](docs/guide/index.html) covers profile locations, configuration and tasks, repository SSH keys, execution results and examples. It distinguishes agreed v2026.11.0 behavior from proposed syntax and prototype capabilities while verification is completed.

## Layout

- `go.mod`, `main.go`: Go module and CLI entry point.
- `cmd/`, `profile/`, `runner/`, `provision/`, `runlog/`, `ui/`: CLI packages.
- `examples/`: sample profiles, snippets and templates.
- `acceptance/`: opt-in Multipass suite.
- `docs/`: landing page and public documentation, deployed as one static site.
- `docs/design/`: current Figcraft source, PNG exports, vector assets, and branding tools; excluded from website deployment.
- `scripts/`: dependency-free website build and validation.

Requires Node.js 24 for website tooling. Run `npm run build` to validate links and generate `dist/`. Open `docs/index.html` locally to review the site without a server. The site is plain HTML, CSS and one small script (`docs/assets/site.js`) for the terminal preview; it makes no third-party requests.

Build and test from this repository root (the directory containing this README):

```sh
go build -o plonk .
go vet ./...
go test -count=1 ./...
```

See [CLI usage](CLI.md) for commands and configuration.
Releases use CalVer (`YYYY.MM.PATCH`), for example `2026.11.0` or `2026.11.0-rc.1`.
For the optional full CLI and website check, see the [Multipass acceptance suite](acceptance/README.md).
Run `go test -tags multipass ./acceptance -v -count=1 -timeout 45m` from this repository root
on Windows or Linux with Multipass installed. It uses a disposable Ubuntu VM;
ordinary Go tests stay fast and do not start VMs.

## GitHub Pages

See [domain and preview-site setup](PAGES.md) for `plonkr.dev` and
`prerelease.plonkr.dev`, including DNS records and repository settings.

Branch roles, checks and release tagging are described in [Branches and releases](BRANCHES.md).

Push this directory as the repository root. Enable Settings → Pages → GitHub Actions. The workflow checks pull requests, builds pushes to main, and offers manual deployment from main. Configure the custom domain as `plonkr.dev`, complete DNS setup with your DNS provider using GitHub's current Pages instructions, then enable HTTPS. The tracked CNAME records the intended domain; DNS has not been changed by this scaffold.

The workflow follows [GitHub's custom Pages workflow guidance](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages). With Actions deployment, configure the domain in repository settings; CNAME alone does not configure it.

The Pegs branding direction is selected and the vector wordmark polish is complete. See [design files](docs/design/README.md) and [web implementation notes](docs/design/web-implementation.md). Runnable commands, platform support and installation instructions await implementation and verification; full v2026.11.0 scope remains open. A licence has not yet been selected.
