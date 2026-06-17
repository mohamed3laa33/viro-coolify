# Viro Web Dashboard

The web dashboard for Viro — a global application platform. Built with Next.js 15
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

The dashboard talks to the Viro Go control-plane. Set the base URL via
`NEXT_PUBLIC_VIRO_API_URL` (defaults to `http://localhost:8080`). When the API is
unreachable, every data view falls back to bundled mock data so the UI renders
standalone — useful for design work and demos.

## Scripts

| Script          | Description                          |
| --------------- | ------------------------------------ |
| `npm run dev`   | Start the dev server                 |
| `npm run build` | Production build                     |
| `npm run start` | Serve the production build           |
| `npm run test`  | Run the Vitest suite (no network)    |

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
- `/dashboard/apps/[uuid]` — app detail (Overview / Logs / Metrics / Environment / Settings)
- `/dashboard/databases` — managed databases
- `/dashboard/domains` — custom domains
- `/dashboard/metrics` — resource metrics
- `/dashboard/settings` — General / Team / Billing

## API contract

Typed in `src/lib/api.ts`. Base URL from `NEXT_PUBLIC_VIRO_API_URL`. All
authenticated calls attach `Authorization: Bearer <token>`.

| Method | Path                                | Notes                       |
| ------ | ----------------------------------- | --------------------------- |
| POST   | `/v1/auth/signup`                   | `{email,name,password}`     |
| POST   | `/v1/auth/login`                    | `{email,password}`          |
| POST   | `/v1/auth/refresh`                  | `{refreshToken}`            |
| GET    | `/v1/me`                            | bearer                      |
| GET    | `/v1/orgs` / POST `/v1/orgs`        | bearer                      |
| GET    | `/v1/apps` / `/v1/apps/{uuid}`      | bearer                      |
| POST   | `/v1/apps/{uuid}/{deploy\|stop\|restart}` | bearer, returns `{status}` |
| GET    | `/v1/databases`                     | bearer                      |

## Notes

- Dark mode is the default (the `dark` class is set on `<html>`).
- Stripe billing is shown in **test mode** in Settings → Billing.
- Auth tokens are stored in `localStorage` via the `AuthProvider` in
  `src/lib/auth.tsx`.
```
