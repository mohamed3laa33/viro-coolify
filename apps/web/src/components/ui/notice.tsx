import { cn } from "@/lib/utils";

export type NoticeVariant = "info" | "warning";

const variantClasses: Record<NoticeVariant, string> = {
  info: "border-primary/30 bg-primary/10 text-primary",
  warning: "border-warning/30 bg-warning/10 text-warning",
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
  ...props
}: NoticeProps) {
  return (
    <div
      role="status"
      className={cn(
        "rounded-md border px-4 py-2 text-sm",
        variantClasses[variant],
        className,
      )}
      {...props}
    />
  );
}
