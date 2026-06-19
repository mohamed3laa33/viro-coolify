"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Boxes, Check, Rocket, ScrollText, Globe2, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import type { App } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

// Per-org dismissal key so the checklist can be dismissed in one org without
// hiding it for others. Mirrors the `viro.*` localStorage namespace used by auth.
const DISMISS_KEY_PREFIX = "viro.onboardingDismissed.";

// SSR-safe localStorage access. We keep a local copy of the guard (rather than
// importing one) so this component stays self-contained; every method is a
// no-op when storage is unavailable (server render, private mode, quota).
function readDismissed(orgId: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(DISMISS_KEY_PREFIX + orgId) === "1";
  } catch {
    return false;
  }
}

function writeDismissed(orgId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(DISMISS_KEY_PREFIX + orgId, "1");
  } catch {
    // Storage full or disabled; the checklist simply reappears next visit.
  }
}

// A `created` app has been provisioned but never deployed; any other status
// means a deploy was attempted (deploying/restarting/running/stopped/error).
function hasDeployedApp(apps: readonly App[]): boolean {
  return apps.some((a) => a.status !== "created");
}

// A step is either "tracked" (its `done` flag is derived from real data) or a
// "guide" pointer (we can't honestly detect completion from the dashboard's
// data, so we never claim it's done — we just link to where it happens).
interface Step {
  icon: LucideIcon;
  title: string;
  description: string;
  href: string;
  // When false, this is a guide step with no completion state (honest: we don't
  // pretend to know whether the user has viewed logs or added a domain).
  tracked: boolean;
  done: boolean;
}

export interface OnboardingChecklistProps {
  /** The org's apps, already loaded by the dashboard. */
  apps: readonly App[];
  /** Active org id; used to scope the dismissal flag. */
  orgId: string | null;
  className?: string;
}

/**
 * First-run getting-started checklist shown on the dashboard home. It is
 * derived entirely from data the dashboard already has (no extra fetches), and
 * auto-hides once the org has a deployed app — the meaningful first milestone.
 * The user can also dismiss it early; dismissal is persisted per org in
 * localStorage.
 *
 * Honesty (invariant #6): only the "create" and "deploy" steps carry real
 * checkmarks because they're provable from the apps list. "View logs" and
 * "Add a domain" are guide pointers — we don't fabricate completion for them.
 */
export function OnboardingChecklist({
  apps,
  orgId,
  className,
}: OnboardingChecklistProps) {
  // Start hidden to avoid a server/client flash; reveal after reading storage.
  const [dismissed, setDismissed] = useState(true);

  useEffect(() => {
    setDismissed(orgId ? readDismissed(orgId) : true);
  }, [orgId]);

  const createdApp = apps.length > 0;
  const deployedApp = hasDeployedApp(apps);

  // Once the org has shipped its first deploy, the checklist has served its
  // purpose — hide it without requiring an explicit dismissal.
  const completedFirstRun = deployedApp;

  if (!orgId || dismissed || completedFirstRun) return null;

  // The first app is where Deploy / Logs / Domains live (those are tabs on the
  // app detail page). Before an app exists, point at the apps list instead.
  const firstApp = apps[0];
  const appHref = firstApp
    ? `/dashboard/apps/${firstApp.id}`
    : "/dashboard/apps";

  const steps: Step[] = [
    {
      icon: Boxes,
      title: "Create your first app",
      description: "Connect a repo or image and pick a plan.",
      href: "/dashboard/apps",
      tracked: true,
      done: createdApp,
    },
    {
      icon: Rocket,
      title: "Deploy it",
      description: "Ship your app to the cluster.",
      href: appHref,
      tracked: true,
      done: deployedApp,
    },
    {
      icon: ScrollText,
      title: "View logs",
      description: "Watch your app's output once it's running.",
      href: appHref,
      tracked: false,
      done: false,
    },
    {
      icon: Globe2,
      title: "Add a domain",
      description: "Route a custom domain to your app.",
      href: "/dashboard/domains",
      tracked: false,
      done: false,
    },
  ];

  const trackedSteps = steps.filter((s) => s.tracked);
  const doneCount = trackedSteps.filter((s) => s.done).length;

  const handleDismiss = () => {
    if (orgId) writeDismissed(orgId);
    setDismissed(true);
  };

  return (
    <Card className={cn("border-primary/30 bg-primary/5", className)}>
      <CardHeader className="flex-row items-start justify-between space-y-0">
        <div className="space-y-1.5">
          <CardTitle>Get started</CardTitle>
          <CardDescription>
            A few steps to ship your first app.
            {trackedSteps.length > 0 ? (
              <span className="ml-1 font-medium text-foreground">
                {doneCount}/{trackedSteps.length} done
              </span>
            ) : null}
          </CardDescription>
        </div>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Dismiss getting-started checklist"
          onClick={handleDismiss}
        >
          <X className="h-4 w-4" aria-hidden="true" />
        </Button>
      </CardHeader>
      <CardContent>
        <ol className="space-y-2">
          {steps.map((step) => {
            const StepIcon = step.icon;
            return (
              <li key={step.title}>
                <Link
                  href={step.href}
                  className="group flex items-center gap-3 rounded-md border border-transparent px-3 py-2.5 transition-colors hover:border-border hover:bg-card focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                >
                  <span
                    className={cn(
                      "flex h-7 w-7 shrink-0 items-center justify-center rounded-full border",
                      step.done
                        ? "border-success/40 bg-success/15 text-success"
                        : "border-border bg-card text-muted-foreground group-hover:text-foreground",
                    )}
                  >
                    {step.done ? (
                      <Check className="h-4 w-4" aria-hidden="true" />
                    ) : (
                      <StepIcon className="h-4 w-4" aria-hidden="true" />
                    )}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p
                      className={cn(
                        "text-sm font-medium",
                        step.done && "text-muted-foreground line-through",
                      )}
                    >
                      {step.title}
                    </p>
                    <p className="truncate text-xs text-muted-foreground">
                      {step.description}
                    </p>
                  </div>
                  {step.tracked && step.done ? (
                    <span className="sr-only">Completed</span>
                  ) : null}
                </Link>
              </li>
            );
          })}
        </ol>
      </CardContent>
    </Card>
  );
}
