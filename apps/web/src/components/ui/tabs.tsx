import { useId, type KeyboardEvent } from "react";

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
 *
 * Implements the WAI-ARIA tabs pattern: a `tablist` of `tab` buttons with
 * roving tabindex (only the active tab is in the tab order) and arrow-key /
 * Home / End keyboard navigation. Each tab exposes a stable `id` so callers
 * that render panels can wire up `aria-controls` / `aria-labelledby`.
 */
export function Tabs<T extends string>({
  tabs,
  active,
  onChange,
  className,
}: TabsProps<T>) {
  const baseId = useId();
  const tabId = (tab: T) => `${baseId}-tab-${tab}`;

  const handleKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
    const current = tabs.indexOf(active);
    if (current === -1) return;

    let next = current;
    switch (e.key) {
      case "ArrowLeft":
      case "ArrowUp":
        next = (current - 1 + tabs.length) % tabs.length;
        break;
      case "ArrowRight":
      case "ArrowDown":
        next = (current + 1) % tabs.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = tabs.length - 1;
        break;
      default:
        return;
    }

    e.preventDefault();
    const nextTab = tabs[next];
    onChange(nextTab);
    // Move focus to follow selection (automatic activation pattern).
    document.getElementById(tabId(nextTab))?.focus();
  };

  return (
    <div className={cn("border-b border-border", className)}>
      <nav role="tablist" className="-mb-px flex gap-6 overflow-x-auto">
        {tabs.map((t) => {
          const selected = t === active;
          return (
            <button
              key={t}
              id={tabId(t)}
              type="button"
              role="tab"
              aria-selected={selected}
              tabIndex={selected ? 0 : -1}
              onClick={() => onChange(t)}
              onKeyDown={handleKeyDown}
              className={cn(
                "whitespace-nowrap border-b-2 px-1 pb-3 text-sm font-medium transition-colors",
                selected
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t}
            </button>
          );
        })}
      </nav>
    </div>
  );
}
