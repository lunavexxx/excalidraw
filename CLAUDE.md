# CLAUDE.md

## Project Structure

This repo is a multi-project container — **the root is not a yarn/Go project**. Each app is self-contained:

- **`apps/web/`** — Frontend: self-contained yarn workspace monorepo
  - **`apps/web/src/`** — Web application (formerly `excalidraw-app/`)
  - **`apps/web/packages/`** — Core packages published to npm as `@excalidraw/*` (excalidraw, element, math, utils, common, …)
  - **`apps/web/examples/`** — Integration examples (NextJS, browser script)
- **`apps/api/`** — Backend: Go (Gin + pgx + golang-migrate)
- **`apps/dev-docs/`** — Standalone Docusaurus docs site (upstream editor docs)
- **`plans/`** — Product roadmap / planning docs (Chinese)

## Development Workflow

All yarn commands run inside `apps/web` (there is no root package.json):

```bash
cd apps/web
yarn test:typecheck  # TypeScript type checking
yarn test:update     # Run all tests (with snapshot updates)
yarn fix             # Auto-fix formatting and linting issues
```

For the Go API:

```bash
cd apps/api
go build ./...
go test ./...
```

## Architecture Notes

- `apps/web` uses Yarn 1 workspaces; internal packages resolve via the `@excalidraw/*` aliases (see `apps/web/vitest.config.mts` and `apps/web/tsconfig.json`)
- Build system: esbuild for packages, Vite for the app
- This is an independent product fork — upstream (excalidraw/excalidraw) is not merged routinely; security fixes are cherry-picked as needed
