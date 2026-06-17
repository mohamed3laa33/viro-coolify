"use client";

import { useState, type FormEvent } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  type Template,
  type TemplateInput,
  type TemplateKind,
} from "@/lib/api";
import { mockTemplates } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { Select } from "@/components/ui/select";

const KINDS: TemplateKind[] = ["service", "database", "app"];

const EMPTY_TEMPLATE: TemplateInput = {
  key: "",
  name: "",
  description: "",
  category: "",
  kind: "service",
  image: "",
  defaultPort: 0,
  active: true,
  sortOrder: 0,
};

export default function AdminCatalogPage() {
  const { authedCall } = useAuth();

  const { data, refetch, usingFallback } = useResource(
    () => authedCall((token, on) => api.listTemplates(token, on)),
    { data: mockTemplates },
    [],
  );
  const templates = data.data;

  const [editing, setEditing] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const editingTemplate =
    editing && editing !== "new"
      ? templates.find((t) => t.key === editing) ?? null
      : null;

  async function onDelete(tpl: Template) {
    if (
      typeof window !== "undefined" &&
      !window.confirm(`Delete template "${tpl.name}"?`)
    ) {
      return;
    }
    setNotice(null);
    try {
      await authedCall((token, on) => api.deleteTemplate(tpl.key, token, on));
      refetch();
    } catch {
      setNotice("Delete failed (API unreachable — demo mode).");
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Catalog"
        description="Launchable templates: services, databases, and one-click apps."
        actions={
          <Button
            onClick={() =>
              setEditing((cur) => (cur === "new" ? null : "new"))
            }
          >
            <Plus className="h-4 w-4" />
            New template
          </Button>
        }
      />

      {usingFallback && (
        <Notice variant="warning">
          Showing demo data — admin API unreachable. Edits won&apos;t persist.
        </Notice>
      )}

      {notice && <Notice variant="error">{notice}</Notice>}

      {editing === "new" && (
        <TemplateForm
          key="new"
          initial={EMPTY_TEMPLATE}
          title="Create template"
          keyEditable
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) =>
                api.createTemplate(input, token, on),
              );
              setEditing(null);
              refetch();
            } catch {
              setNotice("Create failed (API unreachable — demo mode).");
            }
          }}
        />
      )}

      <Card>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-6 py-3 font-medium">Template</th>
                <th className="px-6 py-3 font-medium">Kind</th>
                <th className="px-6 py-3 font-medium">Image</th>
                <th className="px-6 py-3 font-medium">Port</th>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {templates.map((tpl) => (
                <tr key={tpl.key} className="hover:bg-muted/40">
                  <td className="px-6 py-4">
                    <p className="font-medium">{tpl.name}</p>
                    <p className="font-mono text-xs text-muted-foreground">
                      {tpl.key} · {tpl.category || "—"} · sort {tpl.sortOrder}
                    </p>
                  </td>
                  <td className="px-6 py-4">
                    <Badge variant="outline" className="capitalize">
                      {tpl.kind}
                    </Badge>
                  </td>
                  <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                    {tpl.image}
                  </td>
                  <td className="px-6 py-4 tabular-nums text-muted-foreground">
                    {tpl.defaultPort || "—"}
                  </td>
                  <td className="px-6 py-4">
                    <Badge variant={tpl.active ? "success" : "outline"}>
                      {tpl.active ? "Active" : "Inactive"}
                    </Badge>
                  </td>
                  <td className="px-6 py-4">
                    <div className="flex items-center justify-end gap-2">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          setEditing((cur) =>
                            cur === tpl.key ? null : tpl.key,
                          )
                        }
                      >
                        <Pencil className="h-3.5 w-3.5" />
                        Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onDelete(tpl)}
                      >
                        <Trash2 className="h-3.5 w-3.5 text-destructive" />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {editingTemplate && (
        <TemplateForm
          key={editingTemplate.key}
          initial={editingTemplate}
          title={`Edit ${editingTemplate.name}`}
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) =>
                api.updateTemplate(editingTemplate.key, input, token, on),
              );
              setEditing(null);
              refetch();
            } catch {
              setNotice("Update failed (API unreachable — demo mode).");
            }
          }}
        />
      )}
    </div>
  );
}

function TemplateForm({
  initial,
  title,
  keyEditable = false,
  onCancel,
  onSubmit,
}: {
  initial: TemplateInput;
  title: string;
  keyEditable?: boolean;
  onCancel: () => void;
  onSubmit: (input: TemplateInput) => Promise<void>;
}) {
  const [form, setForm] = useState<TemplateInput>(initial);
  const [pending, setPending] = useState(false);

  function set<K extends keyof TemplateInput>(
    key: K,
    value: TemplateInput[K],
  ) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPending(true);
    try {
      await onSubmit(form);
    } finally {
      setPending(false);
    }
  }

  return (
    <Card>
      <CardContent className="pt-6">
        <h3 className="mb-4 text-base font-semibold">{title}</h3>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="tpl-key">Key</Label>
              <Input
                id="tpl-key"
                value={form.key}
                onChange={(e) => set("key", e.target.value)}
                disabled={!keyEditable}
                required
                placeholder="postgresql"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="tpl-name">Name</Label>
              <Input
                id="tpl-name"
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                required
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="tpl-desc">Description</Label>
            <Input
              id="tpl-desc"
              value={form.description}
              onChange={(e) => set("description", e.target.value)}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="tpl-category">Category</Label>
              <Input
                id="tpl-category"
                value={form.category}
                onChange={(e) => set("category", e.target.value)}
                placeholder="Databases"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="tpl-kind">Kind</Label>
              <Select
                id="tpl-kind"
                value={form.kind}
                onChange={(e) => set("kind", e.target.value as TemplateKind)}
              >
                {KINDS.map((k) => (
                  <option key={k} value={k} className="capitalize">
                    {k}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="tpl-image">Image</Label>
              <Input
                id="tpl-image"
                value={form.image}
                onChange={(e) => set("image", e.target.value)}
                placeholder="postgres:16"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="tpl-port">Default port</Label>
              <Input
                id="tpl-port"
                type="number"
                min={0}
                value={form.defaultPort}
                onChange={(e) => set("defaultPort", Number(e.target.value))}
              />
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2 sm:items-end">
            <div className="space-y-2">
              <Label htmlFor="tpl-sort">Sort order</Label>
              <Input
                id="tpl-sort"
                type="number"
                value={form.sortOrder}
                onChange={(e) => set("sortOrder", Number(e.target.value))}
              />
            </div>
            <button
              type="button"
              onClick={() => set("active", !form.active)}
              className="inline-flex h-10 items-center gap-2 text-sm font-medium"
            >
              <span
                className={cn(
                  "relative inline-flex h-5 w-9 items-center rounded-full transition-colors",
                  form.active ? "bg-primary" : "bg-muted",
                )}
              >
                <span
                  className={cn(
                    "inline-block h-4 w-4 transform rounded-full bg-white transition-transform",
                    form.active ? "translate-x-4" : "translate-x-0.5",
                  )}
                />
              </span>
              Active
            </button>
          </div>

          <div className="flex items-center gap-2">
            <Button type="submit" loading={pending}>
              Save template
            </Button>
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
