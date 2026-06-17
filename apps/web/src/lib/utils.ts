import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Merge class names with clsx and de-duplicate conflicting Tailwind utilities.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/**
 * Derive up to two uppercase initials from a person's display name (or email).
 * Empty/blank input yields an empty string.
 */
export function initials(name: string): string {
  return (name ?? "")
    .split(/\s+/)
    .filter(Boolean)
    .map((p) => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

/**
 * Brand magenta as a CSS color usable for inline SVG strokes/fills, backed by
 * the `--brand-magenta` design token (see globals.css / tailwind theme).
 */
export const BRAND_MAGENTA = "hsl(var(--brand-magenta))";

/** Base domain for platform-issued app hostnames. */
export const VORTEX_BASE_DOMAIN = "vortex.v60ai.com";

/**
 * Lowercase a value into a DNS-safe slug. Collapses runs of non-alphanumerics
 * to single hyphens and trims leading/trailing hyphens; falls back to "app".
 */
export function slugify(value: string): string {
  return (
    (value ?? "")
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "app"
  );
}

/**
 * Build the default platform hostname for an app:
 * `<app>.<project>.<org>.vortex.v60ai.com`. The project segment defaults to
 * "default" until apps expose their project.
 */
export function buildAppFqdn(
  appName: string,
  orgSlug: string,
  projectSlug = "default",
): string {
  return `${slugify(appName)}.${slugify(projectSlug)}.${slugify(
    orgSlug,
  )}.${VORTEX_BASE_DOMAIN}`;
}
