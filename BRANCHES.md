# Branches and releases

| Branch | Purpose | Release tags |
| --- | --- | --- |
| `main` | Stable releases | `v2026.11.0`, `v2026.11.1`, etc. |
| `prerelease` | Candidate builds | `v2026.11.0-alpha.1`, `v2026.11.0-beta.1`, `v2026.11.0-rc.1` |
| `develop` | Ongoing work; no releases | None |

Plonkr uses **CalVer: `YYYY.MM.PATCH`**, with a four-digit year, a two-digit
month (`01`–`12`), and a zero-based release counter for that month. The first
release in November 2026 is `2026.11.0`; subsequent releases are `2026.11.1`,
`2026.11.2`, etc. December starts at `2026.12.0`. Use the intended release month;
if a candidate rolls into another month, start a new version and candidate counter.

Candidates append `-alpha.N`, `-beta.N` or `-rc.N`. Tag names add a leading `v`;
the CLI version omits it. Untagged development builds currently identify as
`2026.11.0-beta.1`. This is the beta being prepared; no tag or published release
is implied by the source version. These numbers express release dates and sequence, not semantic
compatibility guarantees; document breaking changes in release notes.

Use feature branches into `develop`, promote tested changes to `prerelease`,
then promote a verified candidate to `main` for stable release. Use merges or
fast-forwards so the release commit remains reachable from the intended branch.
Ordinary branch pushes run checks; they do not create release tags or releases.

The repository was bootstrapped with an empty initial commit on `main`, followed
by the current implementation on `prerelease`. `develop` starts from that candidate.
The initial empty `main` baseline is not a stable software release. A separate
website-only commit makes the beta-status site publishable from `main` before
stable CLI code is promoted.

## Workflows

- **Checks:** Linux vet/race tests, Windows vet/tests, build, tagged acceptance
  harness compilation and website validation on pushes and pull requests to all
  three branches. Multipass VM execution stays opt-in.
- **Release draft:** a pushed version tag must match the stable or prerelease
  naming above and point to a commit reachable from its designated remote branch.
  It reruns checks, builds Linux amd64/arm64 and Windows amd64 archives, embeds
  the version, includes CLI docs/examples, and produces SHA-256 checksums. Only
  after those jobs pass does it create a draft GitHub release, marking candidates
  as prereleases. Review acceptance evidence, assets and notes before publication.
  An existing release is never overwritten by a rerun.
- **Website:** builds branch/PR changes, but deploys to plonkr.dev only from `main`.

Before tagging, run the full suite from the repository root:

```sh
go test -tags multipass ./acceptance -v -count=1 -timeout 45m
```

Create an annotated tag on the chosen commit after promotion, for example
`git tag -a v2026.11.0-beta.1 -m "v2026.11.0-beta.1"` on `prerelease`.
Push the branch before pushing the tag so remote ancestry checks can succeed.
Do not tag `develop`; release checks reject its unpromoted commits.

## GitHub setup still required

The remote is `https://github.com/vanvanni/plonkr.git`; local commits have not
been pushed by this setup. Set the default branch to `develop` while development continues. Protect
all three branches against force pushes/deletion and require PR review and the
Checks jobs before merging. Restrict version-tag creation to maintainers. Configure
Pages and the `github-pages` environment for `main` only. Repository settings,
branch protections and release publication are separate from these workflow files.

The main domain can explain the beta before a stable CLI is released. Promote
only website files and its workflow to `main` for that purpose, keeping unfinished
CLI code on `prerelease`. See [Pages setup](PAGES.md) for this bootstrap and the
independent prerelease site.

The current coverage record is in [the acceptance suite](acceptance/README.md).
Linux arm64 is cross-built; it has not been exercised by that amd64 acceptance run.
Licence selection and repository visibility remain open before public publication.
