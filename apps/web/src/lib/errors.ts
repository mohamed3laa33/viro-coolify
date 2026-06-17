// Single source of truth for turning a thrown value into a user-facing string.
//
// The backend returns structured 4xx/5xx errors with a human-readable message
// (see ApiError in lib/api). We surface that message verbatim so users see the
// real reason a request failed (validation, conflict, forbidden, etc.) instead
// of a generic placeholder.
//
// The `fallback` is reserved for *true* transport failures — a dropped network,
// an aborted request, or a TypeError from fetch — where there is no backend
// message to show. Never use the fallback for a real server error: doing so
// would hide why the action failed (invariant #6: honesty over fake-success).
import { ApiError } from "@/lib/api";

/**
 * Resolve a thrown value into a message suitable for display in a notice.
 *
 * @param err      The caught value (typed `unknown`, as in a catch clause).
 * @param fallback Shown only when the failure is a network/abort/TypeError with
 *                 no backend status — i.e. the request never reached the API or
 *                 returned no structured error.
 */
export function errorMessage(err: unknown, fallback: string): string {
  // Backend 4xx/5xx: surface the server's message when it carries one.
  if (err instanceof ApiError && err.message) {
    return err.message;
  }
  // Anything without a status (network/abort/TypeError) gets the fallback.
  return fallback;
}
