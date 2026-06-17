import { cn } from "@/lib/utils";

export interface SectionProps extends React.HTMLAttributes<HTMLElement> {
  as?: "section" | "div";
}

/**
 * Vertical rhythm wrapper for marketing/page sections with a centered
 * max-width container.
 */
export function Section({
  className,
  children,
  as: Tag = "section",
  ...props
}: SectionProps) {
  return (
    <Tag className={cn("py-20 sm:py-28", className)} {...props}>
      <div className="mx-auto w-full max-w-6xl px-6">{children}</div>
    </Tag>
  );
}
