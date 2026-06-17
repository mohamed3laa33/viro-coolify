"use client";

import Link from "next/link";
import { Check } from "lucide-react";
import { api, type Plan } from "@/lib/api";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

function priceLabel(plan: Plan): { price: string; period: string } {
  if (plan.priceCents <= 0) return { price: "$0", period: "/mo" };
  const dollars = plan.priceCents / 100;
  const price = dollars.toLocaleString(undefined, {
    style: "currency",
    currency: (plan.currency || "usd").toUpperCase(),
    minimumFractionDigits: dollars % 1 === 0 ? 0 : 2,
  });
  return { price, period: "/mo" };
}

function planFeatures(plan: Plan): string[] {
  const features: string[] = [];
  features.push(
    `${plan.includedHours.toLocaleString()} included machine-hours`,
  );
  if (typeof plan.maxApps === "number") {
    // A non-positive maxApps (0 or negative) is the "unlimited" sentinel;
    // any positive value is the real per-org app cap to display.
    const unlimited = plan.maxApps <= 0;
    features.push(
      unlimited
        ? "Unlimited apps"
        : `Up to ${plan.maxApps} app${plan.maxApps === 1 ? "" : "s"}`,
    );
  }
  if (typeof plan.maxCpu === "number" && typeof plan.maxMemoryMb === "number") {
    features.push(`${plan.maxCpu} vCPU · ${plan.maxMemoryMb}MB RAM`);
  }
  if (plan.overagePerHourCents > 0) {
    const overage = (plan.overagePerHourCents / 100).toLocaleString(undefined, {
      style: "currency",
      currency: (plan.currency || "usd").toUpperCase(),
    });
    features.push(`${overage} per overage hour`);
  }
  return features;
}

export function PricingPlans() {
  // Demo fallback loads lazily (demo mode only); prod shows a real empty state.
  // Plans always come from the API — never hardcoded (invariant #1).
  const demoPlans = useDemoData((m) => m.mockPlans, [] as Plan[]);

  const { data, loading } = useResource(
    () => api.getPlans(),
    { data: demoPlans, provider: "stripe" },
    [demoPlans],
    { cacheKey: "plans" },
  );

  // Show active plans (when the flag is present) sorted by sortOrder.
  const plans = [...data.data]
    .filter((p) => p.active !== false)
    .sort((a, b) => (a.sortOrder ?? 0) - (b.sortOrder ?? 0));

  if (loading) {
    return (
      <div className="mt-14 grid gap-6 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Card key={i} className="p-8">
            <Skeleton className="h-6 w-24" />
            <Skeleton className="mt-4 h-10 w-32" />
            <Skeleton className="mt-3 h-4 w-full" />
            <div className="mt-6 space-y-3">
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-4 w-1/2" />
            </div>
            <Skeleton className="mt-8 h-10 w-full" />
          </Card>
        ))}
      </div>
    );
  }

  if (plans.length === 0) {
    return (
      <p className="mt-14 text-center text-sm text-muted-foreground">
        No plans are available right now.
      </p>
    );
  }

  // Mark the middle plan as featured for emphasis.
  const featuredIndex = plans.length > 1 ? 1 : 0;

  return (
    <div className="mt-14 grid gap-6 lg:grid-cols-3">
      {plans.map((plan, i) => {
        const featured = i === featuredIndex;
        const { price, period } = priceLabel(plan);
        return (
          <Card
            key={plan.id}
            className={cn(
              "relative p-8",
              featured && "border-primary/50 glow-violet",
            )}
          >
            {featured && (
              <span className="absolute right-6 top-6 rounded-full bg-brand-balloon px-2.5 py-0.5 text-xs font-medium text-white">
                Popular
              </span>
            )}
            <h3 className="text-lg font-semibold">{plan.name}</h3>
            <div className="mt-4 flex items-baseline gap-1">
              <span className="text-4xl font-bold tracking-tight">{price}</span>
              <span className="text-sm text-muted-foreground">{period}</span>
            </div>
            <p className="mt-2 text-sm text-muted-foreground">
              {plan.description}
            </p>
            <ul className="mt-6 space-y-3 text-sm">
              {planFeatures(plan).map((feat) => (
                <li key={feat} className="flex items-center gap-3">
                  <Check className="h-4 w-4 text-success" />
                  <span className="text-muted-foreground">{feat}</span>
                </li>
              ))}
            </ul>
            <Link href="/signup" className="mt-8 block">
              <Button
                variant={featured ? "primary" : "secondary"}
                className="w-full"
              >
                {plan.priceCents <= 0
                  ? "Start free"
                  : `Start with ${plan.name}`}
              </Button>
            </Link>
          </Card>
        );
      })}
    </div>
  );
}
