import { defineConfig, devices } from "@playwright/test";

// Playwright drives end-to-end smoke tests against a running Vortex web app.
// It is intentionally isolated from the vitest unit suite: `npm test` only runs
// vitest (browser-free), while `npm run test:e2e` runs these specs. The CI
// pipeline wires e2e into a dispatch-only job so the fast unit gate stays clean.
//
// Configuration is env-driven so the same config works locally, against a
// preview deployment, or in CI without code changes:
//   E2E_BASE_URL   target origin (default http://localhost:3000)
//   E2E_WEB_SERVER set to "1" to have Playwright start `next dev` itself
//   E2E_EMAIL / E2E_PASSWORD  credentials for the authenticated smoke flow
//     (when unset, the auth/create-app smoke is skipped rather than failing —
//      these specs must not invent fake-success against a real backend).

const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:3000";

// Only manage a dev server when explicitly asked; by default we assume the app
// is already running (or reachable at E2E_BASE_URL) so e2e never accidentally
// boots a server in environments that don't want one.
const manageServer = process.env.E2E_WEB_SERVER === "1";

export default defineConfig({
  testDir: "e2e",
  // Fail the build if test.only is committed.
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  // Keep CI deterministic; allow parallelism locally.
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: manageServer
    ? {
        command: "npm run dev",
        url: baseURL,
        timeout: 120_000,
        reuseExistingServer: !process.env.CI,
      }
    : undefined,
});
