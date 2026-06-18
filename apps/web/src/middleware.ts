import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// Build a connect-src that allows the app's own origin plus the API origin.
// The API URL is known at build time via NEXT_PUBLIC_VORTEX_API_URL; if it is
// unset (or malformed) we fall back to a self-only policy so we never emit a
// permissive default.
function apiConnectSrc(): string {
  const raw = process.env.NEXT_PUBLIC_VORTEX_API_URL;
  if (!raw) return "'self'";
  try {
    const { origin } = new URL(raw);
    return `'self' ${origin}`;
  } catch {
    return "'self'";
  }
}

// Defense-in-depth: auth tokens are HttpOnly cookies (api.ts sends
// credentials:'include'), so they are never readable by page scripts. The
// Content-Security-Policy is still our main lever to reduce the blast radius of
// an XSS bug — it constrains where scripts may load from and where data may be
// exfiltrated to (connect-src).
//
// A static `script-src 'self'` would block Next.js's inline hydration scripts,
// leaving the production app non-interactive. Instead we mint a fresh nonce per
// request and pass it to Next via a request header so it stamps every inline
// <script> with `nonce-<n>`; the response CSP then trusts only that nonce plus
// `strict-dynamic`, so scripts loaded by nonced scripts are trusted while
// arbitrary injected scripts are not. 'unsafe-inline' remains only on style-src,
// because Next.js / Tailwind inject inline <style> at runtime.
export function middleware(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString("base64");

  const csp = [
    "default-src 'self'",
    "base-uri 'self'",
    "object-src 'none'",
    "frame-ancestors 'none'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    `connect-src ${apiConnectSrc()}`,
    "form-action 'self'",
  ].join("; ");

  // Forward the nonce + CSP on the *request* headers so Next.js can read them
  // and inject the nonce into the scripts it renders for this request.
  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-nonce", nonce);
  requestHeaders.set("Content-Security-Policy", csp);

  const response = NextResponse.next({
    request: { headers: requestHeaders },
  });
  // Emit the same policy on the response so the browser enforces it.
  response.headers.set("Content-Security-Policy", csp);
  return response;
}

export const config = {
  matcher: [
    // Run on all routes except static assets and the favicon, which do not
    // execute scripts and so do not need a per-request nonce.
    {
      source:
        "/((?!_next/static|_next/image|favicon.ico|robots.txt|sitemap.xml).*)",
      missing: [
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
};
