import type { LucideIcon } from "lucide-react";
import { Card } from "@/components/ui/card";

export interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: React.ReactNode;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
}: EmptyStateProps) {
  return (
    <Card className="flex flex-col items-center justify-center px-6 py-20 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary">
        <Icon className="h-6 w-6" />
      </div>
      <h3 className="mt-5 text-base font-semibold">{title}</h3>
      <p className="mt-2 max-w-sm text-sm text-muted-foreground">
        {description}
      </p>
      {action && <div className="mt-6">{action}</div>}
    </Card>
  );
}
