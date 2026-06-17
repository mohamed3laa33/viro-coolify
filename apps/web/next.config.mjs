// Build a connect-src that allows the app's own origin plus the API origin.
// The API URL is known at build time via NEXT_PUBLIC_VORTEX_API_URL; if it is
// unset (or malformed) we fall back to a self-only policy so we never emit a
// permissive default.
function apiConnectSrc() {
  const raw = process.env.NEXT_PUBLIC_VORTEX_API_URL;
  if (!raw) return "'self'";
  try {
    const { origin } = new URL(raw);
    return `'self' ${origin}`;
  } catch {
    return "'self'";
  }
}

// Defense-in-depth: auth tokens live in localStorage (readable by any script
// running on the page), so a strict Content-Security-Policy is our main lever
// to reduce the blast radius of an XSS bug — it constrains where scripts may
// load from and where data may be exfiltrated to (connect-src). 'unsafe-inline'
// is required for style-src only, because Next.js / Tailwind inject inline
// <style> at runtime; scripts are NOT granted 'unsafe-inline'.
const csp = [
  "default-src 'self'",
  "base-uri 'self'",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  `connect-src ${apiConnectSrc()}`,
  "form-action 'self'",
].join("; ");

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Strict-Transport-Security",
    value: "max-age=63072000; includeSubDomains; preload",
  },
];

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // Emit a self-contained server bundle for the production Docker image.
  output: "standalone",
  async headers() {
    return [
      {
        source: "/:path*",
        headers: securityHeaders,
      },
    ];
  },
};

export default nextConfig;
