# Vortex Web Dashboard

The web dashboard for Vortex — a global application platform. Built with Next.js 15
(App Router), React 19, TypeScript, and Tailwind CSS, styled as a dark,
fly.io-grade control panel.

## Stack

- **Next.js 15.1.6** (App Router, React Server Components)
- **React 19**
- **TypeScript 5.7**
- **Tailwind CSS 3.4** + `tailwindcss-animate`
- **lucide-react** for icons
- **Vitest** + Testing Library for tests

## Getting started

```bash
# from apps/web
npm install

# copy env and point it at your control-plane
cp .env.local.example .env.local

# run the dev server
npm run dev          # http://localhost:3000
```

The dashboard talks to the Vortex Go control-plane. Set the base URL via
`NEXT_PUBLIC_VORTEX_API_URL` (defaults to `http://localhost:8080`; the legacy
`NEXT_PUBLIC_VIRO_API_URL` is honored as a fallback). When the API is
unreachable, every data view falls back to bundled mock data so the UI renders
standalone — useful for design work and demos.

## Scripts

| Script          | Description                       |
| --------------- | --------------------------------- |
| `npm run dev`   | Start the dev server              |
| `npm run build` | Production build                  |
| `npm run start` | Serve the production build        |
| `npm run test`  | Run the Vitest suite (no network) |

## Project layout

```
src/
  app/                     App Router routes
    (auth)/                login / signup (route group, no /auth prefix)
    dashboard/             authenticated app shell
  components/              UI primitives + composed components
    ui/                    Button, Card, Badge, Input, Label, StatusDot
  lib/                     api client, auth provider, utils, mock data
```

## Routes

- `/` — marketing landing page
- `/login`, `/signup` — auth cards (store tokens, redirect to `/dashboard`)
- `/dashboard` — overview (stat cards + recent apps)
- `/dashboard/apps` — apps grid
- `/dashboard/apps/[uuid]` — app detail (Overview / Logs / Metrics / Releases /
  Builds / Environment / Domains / Settings). Logs support a live follow/tail SSE
  stream; Metrics render live pod CPU/mem (honest "metrics unavailable" when the
  metrics-server reports nothing); Releases list deploy history + rollback; Builds
  list git-source builds + logs; Domains add/verify/remove with DNS instructions;
  Environment supports a SECRET flag (secret values are never returned); Settings
  exposes update (image/cpu/memory/git) + scale (min/max replicas).
- `/dashboard/databases` — managed databases, with lifecycle
  start/stop/restart/delete and per-row connection info (host/port/db/user/password
  with copy + masked reveal)
- `/dashboard/domains` — custom domains
- `/dashboard/metrics` — resource metrics
- `/dashboard/settings` — General / Team / Billing / API Tokens / Audit
  (Billing shows the current-period charge breakdown; API Tokens create/list/revoke
  PATs with a one-time `vrt_` secret display; Audit is a paginated org/platform log)

## API contract

Typed in `src/lib/api.ts`. Base URL from `NEXT_PUBLIC_VORTEX_API_URL`. All
authenticated calls attach `Authorization: Bearer <token>`. Resource endpoints
are org-scoped under `/v1/orgs/{orgId}/...`; admin endpoints under `/v1/admin/*`.

| Method | Path                                                       | Notes                       |
| ------ | ---------------------------------------------------------- | --------------------------- |
| POST   | `/v1/auth/signup`                                          | `{email,name,password}`     |
| POST   | `/v1/auth/login`                                           | `{email,password}`          |
| POST   | `/v1/auth/refresh`                                         | `{refreshToken}`            |
| GET    | `/v1/me`                                                   | `{id,email,name,isAdmin}`   |
| GET    | `/v1/orgs` · POST `/v1/orgs`                               | bearer                      |
| GET    | `/v1/orgs/{orgId}/apps` · `/{appId}`                       | bearer                      |
| POST   | `/v1/orgs/{orgId}/apps/{appId}/{deploy\|stop\|restart}`    | bearer, returns the App     |
| PATCH  | `/v1/orgs/{orgId}/apps/{appId}`                            | update image/cpu/mem/git    |
| POST   | `/v1/orgs/{orgId}/apps/{appId}/scale`                      | `{minReplicas,maxReplicas}` |
| GET    | `/v1/orgs/{orgId}/apps/{appId}/releases`                   | paginated deploy history    |
| POST   | `/v1/orgs/{orgId}/apps/{appId}/rollback`                   | `{revision}` (optional)     |
| GET    | `/v1/orgs/{orgId}/apps/{appId}/metrics`                    | live pod CPU/mem snapshot   |
| GET    | `/v1/orgs/{orgId}/apps/{appId}/logs?follow=true`           | SSE live log stream         |
| GET    | `/v1/orgs/{orgId}/apps/{appId}/builds` · `/{buildId}`      | build history + logs        |
| PUT    | `/v1/orgs/{orgId}/apps/{appId}/env`                        | `{key,value,secret?}`       |
| POST   | `/v1/orgs/{orgId}/apps/{appId}/domains` · `/{id}/verify`   | add/verify custom domain    |
| GET    | `/v1/orgs/{orgId}/databases/{id}`                          | + connection info           |
| POST   | `/v1/orgs/{orgId}/databases/{id}/{deploy\|stop\|restart}`  | lifecycle                   |
| GET    | `/v1/orgs/{orgId}/billing`                                 | charge breakdown            |
| GET    | `/v1/orgs/{orgId}/audit` · `/v1/admin/audit`               | paginated audit log         |
| POST   | `/v1/tokens` · GET `/v1/tokens` · DELETE `/v1/tokens/{id}` | personal access tokens      |
| GET    | `/v1/services/catalog`                                     | public catalog              |
| GET    | `/v1/billing/plans`                                        | public plan catalog         |

## Notes

- Dark mode is the default (the `dark` class is set on `<html>`).
- Stripe billing is shown in **test mode** in Settings → Billing.
- Auth tokens are stored in `localStorage` via the `AuthProvider` in
  `src/lib/auth.tsx`.

```

```
