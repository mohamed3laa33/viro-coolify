"use client";

import Link from "next/link";
import { Check } from "lucide-react";
import { api, type Plan } from "@/lib/api";
import { mockPlans } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { Button } from "@/components/ui/button";

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
  features.push(`${plan.includedHours.toLocaleString()} included machine-hours`);
  if (typeof plan.maxApps === "number") {
    features.push(
      plan.maxApps >= 100
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
  const { data } = useResource(
    () => api.getPlans(),
    { data: mockPlans, provider: "stripe" },
    [],
  );

  // Show active plans (when the flag is present) sorted by sortOrder.
  const plans = [...data.data]
    .filter((p) => p.active !== false)
    .sort((a, b) => (a.sortOrder ?? 0) - (b.sortOrder ?? 0));

  // Mark the middle plan as featured for emphasis.
  const featuredIndex = plans.length > 1 ? 1 : 0;

  return (
    <div className="mt-14 grid gap-6 lg:grid-cols-3">
      {plans.map((plan, i) => {
        const featured = i === featuredIndex;
        const { price, period } = priceLabel(plan);
        return (
          <div
            key={plan.id}
            className={
              featured
                ? "relative rounded-2xl border border-primary/50 bg-card p-8 glow-violet"
                : "relative rounded-2xl border border-border bg-card p-8"
            }
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
                {plan.priceCents <= 0 ? "Start free" : `Start with ${plan.name}`}
              </Button>
            </Link>
          </div>
        );
      })}
    </div>
  );
}
