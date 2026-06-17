import { cn } from "@/lib/utils";
import { statusVariant, type StatusVariant } from "@/lib/api";

const kindClasses: Record<StatusVariant, string> = {
  success: "bg-success shadow-[0_0_0_3px_hsl(var(--success)/0.2)]",
  muted: "bg-muted-foreground shadow-[0_0_0_3px_hsl(var(--muted)/0.5)]",
  destructive:
    "bg-destructive shadow-[0_0_0_3px_hsl(var(--destructive)/0.2)]",
  warning: "bg-warning shadow-[0_0_0_3px_hsl(var(--warning)/0.2)]",
  info: "bg-info shadow-[0_0_0_3px_hsl(var(--info)/0.2)]",
};

function label(status: string): string {
  if (!status) return "Unknown";
  return status.charAt(0).toUpperCase() + status.slice(1);
}

export interface StatusDotProps {
  status: string;
  showLabel?: boolean;
  className?: string;
  pulse?: boolean;
}

export function StatusDot({
  status,
  showLabel = false,
  className,
  pulse = true,
}: StatusDotProps) {
  const kind = statusVariant(status);
  const animate = pulse && kind === "success";

  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <span className="relative flex h-2.5 w-2.5">
        {animate && (
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success/60 motion-reduce:animate-none" />
        )}
        <span
          className={cn(
            "relative inline-flex h-2.5 w-2.5 rounded-full",
            kindClasses[kind],
          )}
        />
      </span>
      {showLabel && (
        <span className="text-sm text-muted-foreground">{label(status)}</span>
      )}
    </span>
  );
}
