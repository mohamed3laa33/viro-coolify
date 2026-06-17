"use client";

import { useCallback, useEffect, useId, useRef } from "react";

import { Button } from "@/components/ui/button";

export interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Styles the confirm button as a destructive action when true. */
  destructive?: boolean;
  /**
   * While true, both buttons are disabled (confirm shows the Button spinner)
   * and Escape / backdrop clicks are short-circuited so an in-flight action
   * cannot be dismissed mid-request.
   */
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/**
 * Shared accessible confirmation modal. Replaces the previously divergent
 * inline alert dialogs and every `window.confirm` call site.
 *
 * Accessibility: `role="alertdialog"` + `aria-modal`, `useId`-based
 * `aria-labelledby`/`aria-describedby`, Tab focus-trap within the dialog,
 * initial focus on the safe (Cancel) action, focus restored to the previously
 * focused element on close, and body scroll-lock while open. Escape and
 * backdrop clicks call `onCancel` but become no-ops while `loading`.
 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  destructive = false,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const titleId = useId();
  const descId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const cancelRef = useRef<HTMLButtonElement | null>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);

  // Body scroll-lock, initial focus on Cancel, and focus restore on close —
  // only while open.
  useEffect(() => {
    if (!open) return;

    previouslyFocused.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Land keyboard users on the safe action.
    cancelRef.current?.focus();

    return () => {
      document.body.style.overflow = prevOverflow;
      previouslyFocused.current?.focus();
    };
  }, [open]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (e.key === "Escape") {
        if (loading) return;
        e.preventDefault();
        onCancel();
        return;
      }
      // Trap Tab focus within the dialog.
      if (e.key === "Tab") {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input, [tabindex]:not([tabindex="-1"])',
        );
        if (!focusable || focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        const activeEl = document.activeElement;
        if (e.shiftKey && activeEl === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && activeEl === last) {
          e.preventDefault();
          first.focus();
        }
      }
    },
    [loading, onCancel],
  );

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !loading) onCancel();
      }}
    >
      <div
        ref={dialogRef}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        onKeyDown={onKeyDown}
        className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-lg"
      >
        <h2
          id={titleId}
          className={
            destructive
              ? "text-lg font-semibold text-destructive"
              : "text-lg font-semibold text-foreground"
          }
        >
          {title}
        </h2>
        {description ? (
          <p id={descId} className="mt-2 text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
        <div className="mt-6 flex justify-end gap-2">
          <Button
            ref={cancelRef}
            variant="secondary"
            size="sm"
            onClick={onCancel}
            disabled={loading}
          >
            {cancelLabel}
          </Button>
          <Button
            variant={destructive ? "destructive" : "primary"}
            size="sm"
            onClick={onConfirm}
            loading={loading}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
