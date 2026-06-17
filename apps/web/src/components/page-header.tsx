import { cn } from "@/lib/utils";

export interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
  /**
   * Heading level to render the title as. Defaults to "h1".
   * Use "h2" when the page already renders an h1 elsewhere to avoid
   * duplicate-h1 accessibility issues (e.g. Metrics).
   */
  as?: "h1" | "h2";
}

export function PageHeader({
  title,
  description,
  actions,
  className,
  as: Heading = "h1",
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between",
        className,
      )}
    >
      <div>
        <Heading className="text-2xl font-semibold tracking-tight">
          {title}
        </Heading>
        {description && (
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
