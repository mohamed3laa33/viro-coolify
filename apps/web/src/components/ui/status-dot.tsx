import { cn } from "@/lib/utils";
import type { AppStatus } from "@/lib/api";

type StatusKind = "success" | "muted" | "destructive" | "warning";

const STATUS_MAP: Record<string, { kind: StatusKind; label: string }> = {
  running: { kind: "success", label: "Running" },
  stopped: { kind: "muted", label: "Stopped" },
  error: { kind: "destructive", label: "Error" },
  deploying: { kind: "warning", label: "Deploying" },
};

const kindClasses: Record<StatusKind, string> = {
  success: "bg-success shadow-[0_0_0_3px_hsl(var(--success)/0.2)]",
  muted: "bg-muted-foreground shadow-[0_0_0_3px_hsl(var(--muted)/0.5)]",
  destructive:
    "bg-destructive shadow-[0_0_0_3px_hsl(var(--destructive)/0.2)]",
  warning: "bg-warning shadow-[0_0_0_3px_hsl(var(--warning)/0.2)]",
};

export interface StatusDotProps {
  status: AppStatus | string;
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
  const meta = STATUS_MAP[status] ?? { kind: "muted", label: status };
  const animate = pulse && meta.kind === "success";

  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <span className="relative flex h-2.5 w-2.5">
        {animate && (
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success/60" />
        )}
        <span
          className={cn(
            "relative inline-flex h-2.5 w-2.5 rounded-full",
            kindClasses[meta.kind],
          )}
        />
      </span>
      {showLabel && (
        <span className="text-sm text-muted-foreground">{meta.label}</span>
      )}
    </span>
  );
}
