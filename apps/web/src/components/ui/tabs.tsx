import { cn } from "@/lib/utils";

export interface TabsProps<T extends string> {
  tabs: readonly T[];
  active: T;
  onChange: (tab: T) => void;
  className?: string;
}

/**
 * Underlined tab bar shared between the settings and app-detail pages. Purely
 * presentational — the caller owns the active-tab state.
 */
export function Tabs<T extends string>({
  tabs,
  active,
  onChange,
  className,
}: TabsProps<T>) {
  return (
    <div className={cn("border-b border-border", className)}>
      <nav className="-mb-px flex gap-6 overflow-x-auto">
        {tabs.map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => onChange(t)}
            className={cn(
              "whitespace-nowrap border-b-2 px-1 pb-3 text-sm font-medium transition-colors",
              t === active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t}
          </button>
        ))}
      </nav>
    </div>
  );
}
