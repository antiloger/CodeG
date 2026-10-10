# codeg web UI

React + TypeScript + Vite, styled with [shadcn/ui](https://ui.shadcn.com) (the
`base-maia` style on Base UI) and the theme in `src/index.css`. The graph is
[React Flow](https://reactflow.dev); layout is [ELK](https://github.com/kieler/elkjs)
in a web worker.

The UI is a client of the JSON API served by `codeg serve`. It is embedded into
the Go binary (`internal/webui`), so a release is one file.

## Build

```sh
npm ci
npm run build      # writes ../internal/webui/dist, then rebuild the Go binary
```

Without a build, `codeg serve` shows a placeholder page and the API still works.

## Develop

```sh
codeg index                                        # in the Go module to look at
codeg serve -dev-origin http://localhost:5173      # the API, on 127.0.0.1:7373
npm run dev                                        # Vite; /api is proxied to it
```

The server only accepts requests whose Host and Origin are its own address (a
defence against DNS rebinding); `-dev-origin` allows the Vite dev server, and
the proxy in `vite.config.ts` rewrites Host with `changeOrigin`.

## Test

```sh
npm test           # unit and component tests (Vitest, Testing Library)
npm run lint
npm run typecheck
npm run e2e        # real browser against a running `codeg serve`; see e2e/tour.mjs
```

## Adding components

Use the shadcn CLI, not hand-written copies, so components stay in sync with the
registry:

```sh
npx shadcn@latest add <component>
```

Import `cn` from `@/lib/utils`. Colors come from the theme's tokens only; the
chart tokens are a single hue, so graph edge kinds are told apart by color
(primary, destructive, foreground, muted) and dash pattern (`src/lib/graph-style.ts`).

## How it is organised

- `src/api` – types mirroring the Go JSON, the fetch client, React Query hooks
- `src/lib/url-state.ts` – the whole view lives in the URL query string (symbol keys contain dots, so never in the path)
- `src/lib/layout.ts` – pure layout: clusters and packs nodes; ELK only lays out the clusters
- `src/lib/collapse.ts` – imported packages collapse to one node until expanded
- `src/components/graph` – nodes, edges, canvas, toolbar
- `src/components/sidebar` – symbol details: overview, source, references, variable events
