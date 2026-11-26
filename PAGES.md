# Pages setup for plonkr.dev

Source repository: [vanvanni/plonkr](https://github.com/vanvanni/plonkr).
The software is beta; the site copy says so and must not imply a stable CLI.

## Deployment

The **Website** workflow (`.github/workflows/pages.yml`) builds `dist/` on every
push and PR, and deploys to GitHub Pages only from **`prerelease`**. When a
stable release lands on `main`, change the `deploy` job's `github.ref` check
back to `refs/heads/main` and update the environment policy below.

Repository settings:

1. **Settings → Pages → Source:** GitHub Actions.
2. **Settings → Pages → Custom domain:** `plonkr.dev` (the `docs/CNAME` file is
   copied into `dist/`, but Actions deployments still need this setting).
   Enable **Enforce HTTPS** once the certificate is issued.
3. **Settings → Environments → github-pages → Deployment branches:** allow
   `prerelease`.

## DNS (Porkbun)

| Type | Name | Value |
| --- | --- | --- |
| A | @ | 185.199.108.153 |
| A | @ | 185.199.109.153 |
| A | @ | 185.199.110.153 |
| A | @ | 185.199.111.153 |
| AAAA | @ | 2606:50c0:8000::153 |
| AAAA | @ | 2606:50c0:8001::153 |
| AAAA | @ | 2606:50c0:8002::153 |
| AAAA | @ | 2606:50c0:8003::153 |
| CNAME | www | vanvanni.github.io |

Optionally verify `plonkr.dev` under your GitHub account's **Settings → Pages**
using the TXT record GitHub provides, and keep that record afterward.
[GitHub custom-domain reference](https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site).
