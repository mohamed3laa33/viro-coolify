import { test, expect } from "@playwright/test";

// End-to-end smoke for the Vortex dashboard. These specs exercise the real app
// against a running web server (and, for the authenticated flow, a real control
// plane). They never assert fake success: the auth/create-app path is skipped
// when credentials are not provided rather than pretending it passed.
//
// Required env for the authenticated flow:
//   E2E_EMAIL, E2E_PASSWORD  — credentials for an existing Vortex account.
// The unauthenticated render check always runs against E2E_BASE_URL.

const email = process.env.E2E_EMAIL;
const password = process.env.E2E_PASSWORD;
const hasCredentials = Boolean(email && password);

test.describe("auth smoke", () => {
  test("login page renders the sign-in form", async ({ page }) => {
    await page.goto("/login");

    await expect(
      page.getByRole("heading", { name: /welcome back/i }),
    ).toBeVisible();
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByRole("button", { name: /log in/i })).toBeVisible();
  });

  test("logs in and lands on the dashboard", async ({ page }) => {
    test.skip(
      !hasCredentials,
      "Set E2E_EMAIL and E2E_PASSWORD to run the authenticated smoke flow.",
    );

    await page.goto("/login");
    await page.getByLabel("Email").fill(email!);
    await page.getByLabel("Password").fill(password!);
    await page.getByRole("button", { name: /log in/i }).click();

    await page.waitForURL("**/dashboard**");
    await expect(
      page.getByRole("heading", { name: /welcome back/i }),
    ).toBeVisible();
  });
});

test.describe("create app smoke", () => {
  test("opens the new-app form from the apps page", async ({ page }) => {
    test.skip(
      !hasCredentials,
      "Set E2E_EMAIL and E2E_PASSWORD to run the authenticated smoke flow.",
    );

    // Authenticate, then drive the create-app form open. We stop short of
    // submitting so the smoke run doesn't create real workloads on the cluster;
    // submission is covered by the API integration suite.
    await page.goto("/login");
    await page.getByLabel("Email").fill(email!);
    await page.getByLabel("Password").fill(password!);
    await page.getByRole("button", { name: /log in/i }).click();
    await page.waitForURL("**/dashboard**");

    await page.goto("/dashboard/apps");
    await page.getByRole("button", { name: /new app/i }).click();

    await expect(page.getByRole("heading", { name: /new app/i })).toBeVisible();
    await expect(page.getByLabel("Name")).toBeVisible();
    await expect(
      page.getByRole("button", { name: /create app/i }),
    ).toBeVisible();
  });
});
