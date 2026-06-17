"use client";

import { useState } from "react";
import { Globe, Plus, ShieldCheck, ShieldAlert } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App, type Domain } from "@/lib/api";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Notice } from "@/components/ui/notice";

// A custom domain joined to the app it routes to.
interface DomainRow extends Domain {
  app: string;
}

export default function DomainsPage() {
  const { activeOrgId, authedCall } = useAuth();

  // Demo fallbacks load lazily (demo mode only); never shipped to prod.
  const demoApps = useDemoData((m) => m.mockApps, [] as App[]);
  const demoDomainRows = useDemoData<DomainRow[]>(
    (m) =>
      m.mockDomains.map((d) => ({ ...d, app: m.mockApps[0]?.name ?? "—" })),
    [],
  );

  // Load the org's apps, then fan out to each app's domains. Falls back to mock
  // data when the API is unreachable or no org is active.
  const { data: appsData } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listApps(activeOrgId, token, on))
      : null,
    { data: demoApps },
    [activeOrgId, demoApps],
    { cacheKey: activeOrgId ? `apps:${activeOrgId}` : undefined },
  );
  const apps = appsData.data;

  const { data: rows, refetch } = useResource<DomainRow[]>(
    activeOrgId
      ? () =>
          authedCall(async (token, on) => {
            const lists = await Promise.all(
              apps.map(async (app: App) => {
                try {
                  const res = await api.listDomains(
                    activeOrgId,
                    app.id,
                    token,
                    on,
                  );
                  return (res.data ?? []).map((d) => ({ ...d, app: app.name }));
                } catch {
                  return [] as DomainRow[];
                }
              }),
            );
            return lists.flat();
          })
      : null,
    demoDomainRows,
    [activeOrgId, apps.map((a) => a.id).join(","), demoDomainRows],
  );

  const domains = rows ?? [];

  // Inline "Add domain" form: the API scopes domains to an app, so the org-wide
  // add works by choosing the target app and posting to its domains endpoint.
  const [formOpen, setFormOpen] = useState(false);
  const [selectedAppId, setSelectedAppId] = useState("");
  const [domainInput, setDomainInput] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const canAdd = apps.length > 0 && activeOrgId !== null;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!activeOrgId) return;
    const appId = selectedAppId || apps[0]?.id;
    const domain = domainInput.trim();
    if (!appId) {
      setFormError("Select an app to route the domain to.");
      return;
    }
    if (!domain) {
      setFormError("Enter a domain name.");
      return;
    }
    setSubmitting(true);
    setFormError(null);
    try {
      await authedCall((token, on) =>
        api.addDomain(activeOrgId, appId, domain, token, on),
      );
      setDomainInput("");
      setSelectedAppId("");
      setFormOpen(false);
      refetch();
    } catch (err) {
      setFormError(
        err instanceof Error ? err.message : "Failed to add domain.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Domains"
        description="Custom domains with automatic, zero-config TLS certificates."
        actions={
          <Button
            onClick={() => {
              setFormError(null);
              setFormOpen((open) => !open);
            }}
            disabled={!canAdd}
            title={
              canAdd
                ? undefined
                : "Create an app first to attach a custom domain"
            }
            aria-expanded={formOpen}
          >
            <Plus className="h-4 w-4" />
            Add domain
          </Button>
        }
      />

      {formOpen && canAdd && (
        <Card>
          <CardContent className="p-6">
            <form onSubmit={handleSubmit} className="space-y-4">
              {formError && <Notice variant="error">{formError}</Notice>}
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label
                    htmlFor="domain-app"
                    className="text-sm font-medium text-foreground"
                  >
                    App
                  </label>
                  <Select
                    id="domain-app"
                    value={selectedAppId || apps[0]?.id || ""}
                    onChange={(e) => setSelectedAppId(e.target.value)}
                  >
                    {apps.map((app: App) => (
                      <option key={app.id} value={app.id}>
                        {app.name}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <label
                    htmlFor="domain-name"
                    className="text-sm font-medium text-foreground"
                  >
                    Domain
                  </label>
                  <Input
                    id="domain-name"
                    placeholder="app.example.com"
                    value={domainInput}
                    onChange={(e) => setDomainInput(e.target.value)}
                    aria-invalid={formError ? true : undefined}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Button type="submit" loading={submitting}>
                  Add domain
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setFormOpen(false)}
                  disabled={submitting}
                >
                  Cancel
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardContent className="p-0">
          {domains.length === 0 ? (
            <p className="px-6 py-10 text-center text-sm text-muted-foreground">
              No custom domains yet. Use{" "}
              <span className="font-medium text-foreground">Add domain</span> to
              attach one to an app.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {domains.map((d) => (
                <li
                  key={d.id}
                  className="flex items-center justify-between px-6 py-4"
                >
                  <div className="flex items-center gap-3">
                    <Globe className="h-4 w-4 text-muted-foreground" />
                    <div>
                      <p className="font-mono text-sm">{d.domain}</p>
                      <p className="text-xs text-muted-foreground">
                        routes to {d.app}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    {d.verified ? (
                      <Badge variant="success">
                        <ShieldCheck className="mr-1 h-3 w-3" />
                        TLS
                      </Badge>
                    ) : (
                      <Badge variant="warning">
                        <ShieldAlert className="mr-1 h-3 w-3" />
                        Pending
                      </Badge>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
