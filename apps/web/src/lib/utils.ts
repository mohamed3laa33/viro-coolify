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

/**
 * Brand violet as a CSS color for inline SVG strokes/fills, backed by the
 * `--brand-violet` design token (see globals.css / tailwind theme).
 */
export const BRAND_VIOLET = "hsl(var(--brand-violet))";

/**
 * Deep brand violet (logo basket / seam accent), backed by the
 * `--brand-violet-deep` design token.
 */
export const BRAND_VIOLET_DEEP = "hsl(var(--brand-violet-deep))";

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

/**
 * Default landing path used when an internal redirect target is missing or
 * unsafe.
 */
export const DEFAULT_NEXT_PATH = "/dashboard";

/**
 * Sanitize a `next` redirect parameter so it can only point at an in-app path.
 *
 * Returns `param` only when it is a same-origin absolute path: it must start
 * with a single "/" (not "//", which is protocol-relative and would navigate
 * off-site) and must not contain ":" (which would allow `javascript:` or
 * absolute URLs like `http://evil.com`). Anything else falls back to
 * {@link DEFAULT_NEXT_PATH}.
 */
export function safeNextPath(param: string | null): string {
  if (
    param &&
    param.startsWith("/") &&
    !param.startsWith("//") &&
    !param.includes(":")
  ) {
    return param;
  }
  return DEFAULT_NEXT_PATH;
}
