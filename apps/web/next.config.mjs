// Security headers that are identical for every request. The
// Content-Security-Policy is intentionally NOT set here: it needs a fresh
// per-request nonce for Next.js's inline hydration scripts, so it is owned
// entirely by src/middleware.ts (the single source of truth for CSP). A static
// `script-src 'self'` would block those inline scripts and leave the production
// app non-interactive.
const securityHeaders = [
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
