import { cn } from "@/lib/utils";

export type SkeletonProps = React.HTMLAttributes<HTMLDivElement>;

/**
 * Loading placeholder: a muted, rounded block that pulses. Size it via
 * `className` (e.g. `h-4 w-32`). Respects `prefers-reduced-motion` by
 * disabling the pulse animation.
 */
export function Skeleton({ className, ...props }: SkeletonProps) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "animate-pulse motion-reduce:animate-none rounded-md bg-muted",
        className,
      )}
      {...props}
    />
  );
}
