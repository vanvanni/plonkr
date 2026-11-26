# Pages setup for plonkr.dev and prerelease.plonkr.dev

Source repository: [vanvanni/plonkr](https://github.com/vanvanni/plonkr).
The software is beta; neither domain should imply that a stable CLI is released.
The checked-in main-site copy says beta, and the preview build adds a prominent
prerelease banner and noindex metadata. No site or DNS has been deployed by these
local changes.

## Main domain

1. Push the local branches when ready: `git push -u origin prerelease`, then
   `git push -u origin develop` and `git push -u origin main`. No release tag is
   necessary for the website.
2. The local `main` branch is prepared with website files independently of the
   beta CLI. For later website updates, on a clean checkout of `main`, run
   `git restore --source prerelease -- docs scripts package.json .github/workflows/pages.yml`,
   then explicitly stage those same paths and commit. Run `npm run check`, then
   push `main`. This makes website files available without promoting the beta CLI.
   Design sources are excluded by the website build allowlist.
3. In the source repository's **Settings → Pages**, set **Source → GitHub Actions**.
   The Website workflow builds `dist/` and deploys only from `main`. In
   **Settings → Environments → github-pages**, restrict deployment to `main`.
4. In your GitHub account's **Settings → Pages**, add and verify `plonkr.dev`
   using the TXT record GitHub provides. Keep that TXT record afterward.
   [Domain verification instructions](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/verifying-your-custom-domain-for-github-pages).
5. In repository **Settings → Pages → Custom domain**, save `plonkr.dev` before
   changing DNS. The `docs/CNAME` file records intent; Actions deployment still
   requires this setting.
6. Add the records below with your DNS provider. Replace conflicting web-hosting
   records for these names; keep unrelated mail and verification records.
7. After a successful deployment and DNS check, enable **Enforce HTTPS**. Check
   `https://plonkr.dev/` and `/guide/`.

| Type | Name | Value |
| --- | --- | --- |
| A | @ | 185.199.108.153 |
| A | @ | 185.199.109.153 |
| A | @ | 185.199.110.153 |
| A | @ | 185.199.111.153 |
| CNAME | www | vanvanni.github.io |
| CNAME | prerelease | vanvanni.github.io |

The `prerelease` CNAME belongs to the second site below. Configure its custom
domain before adding that DNS record. For IPv6, use the current GitHub AAAA
records from the official guide; do not leave unrelated AAAA records on these names.
[GitHub custom-domain and DNS reference](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site).

PowerShell DNS checks:

```powershell
Resolve-DnsName plonkr.dev -Type A
Resolve-DnsName www.plonkr.dev -Type CNAME
Resolve-DnsName prerelease.plonkr.dev -Type CNAME
```

## Independent prerelease site

GitHub Pages provides one site per repository. Two independent domains therefore
use two Pages repositories, even though all maintained software/docs stay here.
The proposed output-only repository is `vanvanni/plonkr-prerelease-site`. It is a
deployment destination, not another software component or source repository.
[Pages site limits](https://docs.github.com/en/pages/getting-started-with-github-pages/what-is-github-pages).

1. Create that repository (public is simplest for Pages availability), initialized
   with a `main` branch. Do not put source code or private context there. Its
   tracked contents will be replaced by generated website output.
2. In the source repository, set Actions variable `PREVIEW_PAGES_REPOSITORY` to
   `vanvanni/plonkr-prerelease-site`. The workflow skips publication until this is set.
3. Create a fine-grained token restricted to the output repository with **Contents:
   read and write**. Store it as source-repository Actions secret
   `PREVIEW_PAGES_TOKEN`; never commit it. Cross-repository pushes cannot use the
   source repository's ordinary `GITHUB_TOKEN`. The user-provided token also allows
   the resulting push to trigger branch-based Pages builds.
4. Restrict the source repository's `prerelease-pages` environment to `prerelease`.
   Push that branch. The **Prerelease website** workflow copies only generated
   output into the destination's `main` branch. First publication replaces any
   destination README or other tracked files; keep this repository output-only.
5. In the output repository's **Settings → Pages**, select **Deploy from a branch**,
   branch **main**, folder **/ (root)**. Save custom domain `prerelease.plonkr.dev`,
   add its CNAME DNS record, then enable HTTPS after provisioning succeeds.
6. Every subsequent `prerelease` push refreshes that site. The preview's CNAME,
   canonical URLs, sitemap and noindex metadata are generated specifically for
   `prerelease.plonkr.dev`; the main-domain source is not rewritten.

The preview workflow can also be run manually with `prerelease` selected after it
is discoverable on the repository's default branch. Neither workflow publishes
CLI releases. Review and publish beta release drafts separately.
