"use client";

import { useEffect } from "react";

// global-error replaces the ROOT layout when an error is thrown while rendering
// it (or anything not caught by a nested error.tsx). Because it stands in for
// the root layout, it MUST render its own <html>/<body>. It also cannot rely on
// the app's design-system components or Tailwind classes being available (the
// failure may be in the layout/providers themselves), so it is intentionally
// self-contained with inline styles — a last-resort fallback that can never
// itself white-screen.
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Best-effort logging; never let logging throw.
    try {
      console.error(error);
    } catch {
      // ignore
    }
  }, [error]);

  return (
    <html lang="en">
      <body
        style={{
          margin: 0,
          minHeight: "100vh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: "#09090b",
          color: "#fafafa",
          fontFamily:
            "ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
          padding: "1rem",
        }}
      >
        <main
          role="alert"
          style={{
            width: "100%",
            maxWidth: "28rem",
            border: "1px solid #27272a",
            borderRadius: "0.75rem",
            padding: "1.5rem",
          }}
        >
          <h1 style={{ fontSize: "1.125rem", margin: "0 0 0.5rem" }}>
            Something went wrong
          </h1>
          <p
            style={{ color: "#a1a1aa", margin: "0 0 1.25rem", lineHeight: 1.5 }}
          >
            An unexpected error occurred. You can try again, and if the problem
            persists, reach out to support.
          </p>
          {error.digest && (
            <p
              style={{
                fontFamily: "ui-monospace, SFMono-Regular, monospace",
                fontSize: "0.75rem",
                color: "#71717a",
                margin: "0 0 1.25rem",
              }}
            >
              Error reference: {error.digest}
            </p>
          )}
          <button
            type="button"
            onClick={() => reset()}
            style={{
              cursor: "pointer",
              borderRadius: "0.5rem",
              border: "none",
              backgroundColor: "#fafafa",
              color: "#09090b",
              padding: "0.5rem 1rem",
              fontSize: "0.875rem",
              fontWeight: 500,
            }}
          >
            Retry
          </button>
        </main>
      </body>
    </html>
  );
}
