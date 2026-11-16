# ratavyr documentation site

Source of <https://ratavyr.bzy-logics.com>, built with [VitePress](https://vitepress.dev). Deployed to GitHub Pages by [`.github/workflows/docs.yml`](../.github/workflows/docs.yml) on every push to `main` that touches `website/`.

```sh
cd website
npm install
npm run dev        # http://localhost:5173 with hot reload
npm run build      # static site in .vitepress/dist/; fails on dead internal links
npm run preview
```

**Windows:** build from a path with an uppercase drive letter (`cd D:\…\website`). From `d:\…`, VitePress fails while rendering with `Cannot read properties of undefined (reading 'imports')`.

## Keeping it accurate

| Page | Source of truth |
|---|---|
| `reference/rest-api.md`, `reference/websocket.md` | `docs/rest-api.md` |
| `reference/errors.md` | `docs/rest-api.md`, `internal/api/rest/http.go` |
| `reference/environment.md` | `internal/config` |
| `development/engine-contract.md` | `contract.lock`, `scripts/generate.sh` |

When `contract.lock` moves to a new tag, update the example in `development/engine-contract.md`.

Sibling sites: [motioncore.bzy-logics.com](https://motioncore.bzy-logics.com), [sindrava.bzy-logics.com](https://sindrava.bzy-logics.com).

## Deployment setup (once)

1. Repository **Settings → Pages → Source: GitHub Actions**.
2. **Custom domain** `ratavyr.bzy-logics.com`, then **Enforce HTTPS**.
3. DNS: `CNAME ratavyr → bzy-logics.github.io`.
