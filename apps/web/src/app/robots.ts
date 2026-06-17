import type { MetadataRoute } from "next";

// Public site origin used to build the absolute sitemap URL. Sourced from
// NEXT_PUBLIC_SITE_URL with a sensible production default.
const siteUrl = (
  process.env.NEXT_PUBLIC_SITE_URL ?? "https://vortex.v60ai.com"
).replace(/\/+$/, "");

export default function robots(): MetadataRoute.Robots {
  return {
    rules: {
      userAgent: "*",
      allow: "/",
    },
    sitemap: `${siteUrl}/sitemap.xml`,
  };
}
