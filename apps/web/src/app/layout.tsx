import type { Metadata, Viewport } from "next";
import { Inter, JetBrains_Mono } from "next/font/google";
import { AuthProvider } from "@/lib/auth";
import "./globals.css";

const inter = Inter({
  subsets: ["latin"],
  variable: "--font-sans",
  display: "swap",
});

const jetbrainsMono = JetBrains_Mono({
  subsets: ["latin"],
  variable: "--font-mono",
  display: "swap",
});

// Public site origin used for absolute metadata URLs (OG/Twitter images,
// canonical). Sourced from NEXT_PUBLIC_SITE_URL with a sensible default.
const siteUrl = (
  process.env.NEXT_PUBLIC_SITE_URL ?? "https://vortex.v60ai.com"
).replace(/\/+$/, "");

const title = "Vortex — Deploy apps close to your users";
const description =
  "Vortex is a global application platform. Ship containers to the edge, scale instantly, and run managed databases with zero-config TLS.";

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title,
  description,
  icons: {
    icon: "/icon",
  },
  openGraph: {
    type: "website",
    siteName: "Vortex",
    title,
    description,
    url: siteUrl,
  },
  twitter: {
    card: "summary_large_image",
    title,
    description,
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
};

// Force dynamic rendering for every route. Our CSP (src/middleware.ts) mints a
// fresh per-request nonce and relies on `strict-dynamic`, which makes the bare
// `'self'` source ignored — so EVERY <script> Next emits must carry the nonce or
// the browser blocks it. Statically prerendered HTML is generated at build time,
// long before any request nonce exists, so its script tags ship without a nonce
// and the standalone production server (output:standalone) serves dead pages:
// zero client JS loads, React never hydrates, the AuthProvider spinner hangs.
// Rendering on every request lets the middleware nonce be stamped into the
// scripts, which keeps the strong nonce + strict-dynamic policy intact.
export const dynamic = "force-dynamic";

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`dark ${inter.variable} ${jetbrainsMono.variable}`}
    >
      <body className="min-h-screen bg-background font-sans text-foreground antialiased">
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}
