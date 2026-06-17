import Link from "next/link";
import {
  Globe2,
  Gauge,
  Database,
  ShieldCheck,
  ArrowRight,
  Check,
} from "lucide-react";
import { MarketingHeader } from "@/components/marketing-header";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/logo";
import { PricingPlans } from "@/components/pricing-plans";

const FEATURES = [
  {
    icon: Globe2,
    title: "Global deploys",
    body: "Run your app in 30+ regions. Vortex routes every request to the nearest healthy instance automatically.",
  },
  {
    icon: Gauge,
    title: "Instant scaling",
    body: "Scale to zero or to thousands of machines in seconds. Pay only for the compute you actually use.",
  },
  {
    icon: Database,
    title: "Managed Postgres",
    body: "Production-ready Postgres, Redis, and MySQL with automated backups and point-in-time recovery.",
  },
  {
    icon: ShieldCheck,
    title: "Zero-config TLS",
    body: "Every app gets automatic HTTPS with managed certificates. Bring your own domain in one command.",
  },
];

const CLI_LINES = [
  { prompt: true, text: "vortex launch" },
  { prompt: false, text: "Detected a Dockerfile — using it to build." },
  {
    prompt: false,
    text: "Provisioning app marketing-site in iad, lhr, sin...",
  },
  { prompt: false, text: "✓ Built image in 18.2s" },
  { prompt: false, text: "✓ Deployed v1 across 3 regions" },
  {
    prompt: false,
    text: "✓ https://marketing-site.acme.personal.vortex.v60ai.com is live",
  },
];

export default function LandingPage() {
  return (
    <div className="min-h-screen bg-background">
      <MarketingHeader />

      {/* Hero */}
      <div className="relative overflow-hidden">
        <div className="absolute inset-0 grid-bg opacity-[0.35]" aria-hidden />
        <div
          className="pointer-events-none absolute left-1/2 top-[-10rem] h-[32rem] w-[32rem] -translate-x-1/2 rounded-full bg-brand-balloon opacity-25 blur-[120px]"
          aria-hidden
        />
        <Section className="relative pb-16 pt-24 sm:pt-32">
          <div className="mx-auto max-w-3xl text-center">
            <div className="mx-auto mb-6 flex w-fit items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-xs text-muted-foreground">
              <span className="h-1.5 w-1.5 rounded-full bg-success" />
              Now serving 30+ regions worldwide
            </div>
            <h1 className="text-balance bg-gradient-to-br from-foreground to-foreground/60 bg-clip-text text-4xl font-bold tracking-tight text-transparent sm:text-6xl">
              Deploy apps close to your users.
            </h1>
            <p className="mx-auto mt-6 max-w-2xl text-balance text-lg text-muted-foreground">
              Vortex takes your container and runs it on a global network of
              machines, milliseconds from every user. No clusters, no YAML
              sprawl — just{" "}
              <span className="text-foreground">vortex launch</span>.
            </p>
            <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
              <Link href="/signup">
                <Button size="lg" className="w-full sm:w-auto">
                  Deploy your first app
                  <ArrowRight className="h-4 w-4" />
                </Button>
              </Link>
              <a href="#cli" className="w-full sm:w-auto">
                <Button
                  variant="secondary"
                  size="lg"
                  className="w-full sm:w-auto"
                >
                  See the CLI
                </Button>
              </a>
            </div>
          </div>

          {/* Floating balloon accent */}
          <div className="mt-16 flex justify-center">
            <div className="animate-float rounded-3xl border border-border bg-card/60 p-8 glow-violet">
              <Logo size={88} />
            </div>
          </div>
        </Section>
      </div>

      {/* Features */}
      <Section id="features" className="border-t border-border">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
            Everything you need to run apps globally
          </h2>
          <p className="mt-4 text-muted-foreground">
            A complete platform — compute, networking, storage, and TLS — that
            disappears into the background.
          </p>
        </div>

        <div className="mt-14 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          {FEATURES.map((f) => {
            const Icon = f.icon;
            return (
              <div
                key={f.title}
                className="group rounded-xl border border-border bg-card p-6 transition-colors hover:border-primary/40"
              >
                <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-primary/15 text-primary">
                  <Icon className="h-5 w-5" />
                </div>
                <h3 className="mt-5 text-base font-semibold">{f.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  {f.body}
                </p>
              </div>
            );
          })}
        </div>
      </Section>

      {/* CLI block */}
      <Section id="cli" className="border-t border-border">
        <div className="grid items-center gap-12 lg:grid-cols-2">
          <div>
            <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
              One command from git to globe.
            </h2>
            <p className="mt-4 text-muted-foreground">
              The Vortex CLI detects your stack, builds an image, and rolls it
              out across regions with health checks and automatic TLS. No
              pipelines to wire up.
            </p>
            <ul className="mt-6 space-y-3 text-sm">
              {[
                "Auto-detected buildpacks & Dockerfiles",
                "Rolling deploys with instant rollback",
                "Secrets and config in one place",
              ].map((item) => (
                <li key={item} className="flex items-center gap-3">
                  <Check className="h-4 w-4 text-success" />
                  <span className="text-muted-foreground">{item}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="overflow-hidden rounded-xl border border-border bg-[#0c0c0f] shadow-2xl">
            <div className="flex items-center gap-2 border-b border-border px-4 py-3">
              <span className="h-3 w-3 rounded-full bg-destructive/70" />
              <span className="h-3 w-3 rounded-full bg-warning/70" />
              <span className="h-3 w-3 rounded-full bg-success/70" />
              <span className="ml-2 font-mono text-xs text-muted-foreground">
                ~/marketing-site
              </span>
            </div>
            <pre className="overflow-x-auto p-5 font-mono text-sm leading-relaxed">
              {CLI_LINES.map((line, i) => (
                <div key={i} className="whitespace-pre">
                  {line.prompt ? (
                    <span>
                      <span className="text-primary">$ </span>
                      <span className="text-foreground">{line.text}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">{line.text}</span>
                  )}
                </div>
              ))}
            </pre>
          </div>
        </div>
      </Section>

      {/* Pricing teaser */}
      <Section id="pricing" className="border-t border-border">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
            Pricing that scales with you
          </h2>
          <p className="mt-4 text-muted-foreground">
            Start free. Upgrade when you ship. Pay only for the compute you run.
          </p>
        </div>

        <PricingPlans />
      </Section>

      {/* Footer */}
      <footer className="border-t border-border">
        <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-6 px-6 py-12 sm:flex-row">
          <div className="flex items-center gap-2">
            <Logo size={24} withWordmark />
          </div>
          <nav className="flex flex-wrap items-center justify-center gap-6 text-sm text-muted-foreground">
            <a href="#features" className="hover:text-foreground">
              Features
            </a>
            <a href="#pricing" className="hover:text-foreground">
              Pricing
            </a>
            <Link href="/login" className="hover:text-foreground">
              Log in
            </Link>
            <Link href="/signup" className="hover:text-foreground">
              Sign up
            </Link>
          </nav>
          <p className="text-xs text-muted-foreground">
            © {new Date().getFullYear()} Vortex. All rights reserved.
          </p>
        </div>
      </footer>
    </div>
  );
}
