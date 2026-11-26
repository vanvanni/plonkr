# Pages setup for plonkr.dev

Source repository: [vanvanni/plonkr](https://github.com/vanvanni/plonkr).
The software is beta; neither domain should imply that a stable CLI is released.
The checked-in site copy says beta. No site or DNS has been deployed by these
local changes.

## Main domain

1. Push the local branches when ready: `git push -u origin prerelease`, then
   `git push -u origin develop` and `git push -u origin main`. No release tag is
   necessary for the website.
2. `main` currently has only an empty bootstrap commit. Before first deployment,
   promote website files independently of the beta CLI: on a clean checkout of `main`, run
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

For IPv6, use the current GitHub AAAA
records from the official guide; do not leave unrelated AAAA records on these names.
[GitHub custom-domain and DNS reference](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site).

PowerShell DNS checks:

```powershell
Resolve-DnsName plonkr.dev -Type A
Resolve-DnsName www.plonkr.dev -Type CNAME
```
