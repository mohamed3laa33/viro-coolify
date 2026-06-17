"use client";

import { Building2, Users } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { mockAdminOverview, mockPlans } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";

function humanizeMetric(metric: string): string {
  // camelCase / snake_case -> "Title Case".
  return metric
    .replace(/[_-]+/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/^\w/, (c) => c.toUpperCase());
}

export default function AdminOverviewPage() {
  const { authedCall } = useAuth();

  const { data: overview, usingFallback } = useResource(
    () => authedCall((token, on) => api.getAdminOverview(token, on)),
    mockAdminOverview,
    [],
  );

  const { data: plansData } = useResource(
    () => authedCall((token, on) => api.listAdminPlans(token, on)),
    { data: mockPlans },
    [],
  );
  const plans = plansData.data;

  function planName(planId: string): string {
    return plans.find((p) => p.id === planId)?.name ?? planId;
  }

  const subsEntries = Object.entries(overview.subscriptionsByPlan);
  const usageEntries = Object.entries(overview.usageTotals);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Admin overview"
        description="Platform-wide totals across every organization."
      />

      {usingFallback && (
        <Notice variant="warning">
          Showing demo data — admin API unreachable.
        </Notice>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <StatCard
          label="Organizations"
          value={overview.orgCount.toLocaleString()}
          icon={Building2}
        />
        <StatCard
          label="Users"
          value={overview.userCount.toLocaleString()}
          icon={Users}
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Subscriptions by plan</CardTitle>
            <CardDescription>
              Active subscriptions grouped by plan.
            </CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {subsEntries.length === 0 ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">
                No subscriptions yet.
              </p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                      <th className="px-6 py-3 font-medium">Plan</th>
                      <th className="px-6 py-3 text-right font-medium">
                        Subscriptions
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {subsEntries.map(([planId, count]) => (
                      <tr key={planId} className="hover:bg-muted/40">
                        <td className="px-6 py-3 font-medium">
                          {planName(planId)}
                          <span className="ml-2 font-mono text-xs text-muted-foreground">
                            {planId}
                          </span>
                        </td>
                        <td className="px-6 py-3 text-right tabular-nums">
                          {count.toLocaleString()}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Usage totals</CardTitle>
            <CardDescription>
              Aggregate metered usage across the platform.
            </CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {usageEntries.length === 0 ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">
                No usage recorded.
              </p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                      <th className="px-6 py-3 font-medium">Metric</th>
                      <th className="px-6 py-3 text-right font-medium">Total</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {usageEntries.map(([metric, total]) => (
                      <tr key={metric} className="hover:bg-muted/40">
                        <td className="px-6 py-3 font-medium">
                          {humanizeMetric(metric)}
                        </td>
                        <td className="px-6 py-3 text-right tabular-nums">
                          {total.toLocaleString()}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
