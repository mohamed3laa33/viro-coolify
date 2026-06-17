import { Info, AlertTriangle, AlertOctagon, CheckCircle2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

export type NoticeVariant = "info" | "warning" | "error" | "success";

const variantClasses: Record<NoticeVariant, string> = {
  info: "border-primary/30 bg-primary/10 text-primary",
  warning: "border-warning/30 bg-warning/10 text-warning",
  error: "border-destructive/30 bg-destructive/10 text-destructive",
  success: "border-success/30 bg-success/10 text-success",
};

const variantIcons: Record<NoticeVariant, LucideIcon> = {
  info: Info,
  warning: AlertTriangle,
  error: AlertOctagon,
  success: CheckCircle2,
};

export interface NoticeProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: NoticeVariant;
}

/**
 * Inline banner used for transient notices (action results, demo-mode hints,
 * fallback warnings). Replaces the repeated `rounded-md border …` markup.
 */
export function Notice({
  className,
  variant = "info",
  children,
  ...props
}: NoticeProps) {
  const Icon = variantIcons[variant];
  return (
    <div
      role={variant === "error" ? "alert" : "status"}
      aria-live={variant === "error" ? "assertive" : "polite"}
      className={cn(
        "flex items-start gap-2 rounded-md border px-4 py-2 text-sm",
        variantClasses[variant],
        className,
      )}
      {...props}
    >
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
