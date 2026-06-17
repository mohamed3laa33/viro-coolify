// Single source of truth for whether mock/demo fallbacks are allowed.
//
// Mock/demo data must never surface in production. It is enabled only when:
//   - the app is not built for production (NODE_ENV !== "production"), OR
//   - the operator explicitly opts in via NEXT_PUBLIC_DEMO_MODE=1.
//
// In production with the flag off, callers should render real empty/error
// states instead of fabricated orgs/apps/billing/secrets.
export function isDemoMode(): boolean {
  return (
    process.env.NODE_ENV !== "production" ||
    process.env.NEXT_PUBLIC_DEMO_MODE === "1"
  );
}
