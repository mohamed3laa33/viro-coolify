"use client";

import { Globe, Plus, ShieldCheck } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { StatusDot } from "@/components/ui/status-dot";

const DOMAINS = [
  { host: "marketing-site.viro.app", app: "marketing-site", tls: "active" },
  { host: "acme.com", app: "marketing-site", tls: "active" },
  { host: "api.acme.com", app: "api-gateway", tls: "active" },
];

export default function DomainsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Domains"
        description="Custom domains with automatic, zero-config TLS certificates."
        actions={
          <Button>
            <Plus className="h-4 w-4" />
            Add domain
          </Button>
        }
      />

      <Card>
        <CardContent className="p-0">
          <ul className="divide-y divide-border">
            {DOMAINS.map((d) => (
              <li
                key={d.host}
                className="flex items-center justify-between px-6 py-4"
              >
                <div className="flex items-center gap-3">
                  <Globe className="h-4 w-4 text-muted-foreground" />
                  <div>
                    <p className="font-mono text-sm">{d.host}</p>
                    <p className="text-xs text-muted-foreground">
                      routes to {d.app}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-4">
                  <Badge variant="success">
                    <ShieldCheck className="mr-1 h-3 w-3" />
                    TLS
                  </Badge>
                  <StatusDot status="running" showLabel />
                </div>
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
