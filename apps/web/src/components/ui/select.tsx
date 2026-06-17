import { forwardRef } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

export type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement>;

/**
 * Styled native <select> that mirrors the Input primitive (height, border,
 * background, focus-visible ring, disabled state, rounding) and adds a chevron
 * affordance. Forwards all native select props + className; children are
 * rendered as <option>s.
 */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, ...props }, ref) => {
    return (
      <div className="relative w-full">
        <select
          ref={ref}
          className={cn(
            "flex h-10 w-full appearance-none rounded-md border border-border bg-surface-2 px-3 py-2 pr-9 text-sm text-foreground shadow-sm transition-colors pointer-coarse:min-h-11",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:border-ring",
            "disabled:cursor-not-allowed disabled:opacity-50",
            "aria-[invalid=true]:border-destructive aria-[invalid=true]:focus-visible:ring-destructive aria-[invalid=true]:focus-visible:border-destructive",
            className,
          )}
          {...props}
        >
          {children}
        </select>
        <ChevronDown
          aria-hidden="true"
          className="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
        />
      </div>
    );
  },
);

Select.displayName = "Select";
