import type { MetadataRoute } from "next";

// Public site origin used to build absolute URLs. Sourced from
// NEXT_PUBLIC_SITE_URL with a sensible production default. Only public,
// unauthenticated routes belong here.
const siteUrl = (
  process.env.NEXT_PUBLIC_SITE_URL ?? "https://vortex.v60ai.com"
).replace(/\/+$/, "");

export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date();
  return [
    {
      url: `${siteUrl}/`,
      lastModified,
      changeFrequency: "weekly",
      priority: 1,
    },
    {
      url: `${siteUrl}/login`,
      lastModified,
      changeFrequency: "monthly",
      priority: 0.5,
    },
    {
      url: `${siteUrl}/signup`,
      lastModified,
      changeFrequency: "monthly",
      priority: 0.5,
    },
  ];
}
