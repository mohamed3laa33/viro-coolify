"use client";

import {
  Suspense,
  useEffect,
  useState,
  type FormEvent,
} from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { CheckCircle2, Loader2, MailCheck } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Invitation } from "@/lib/api";
import { Logo } from "@/components/logo";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

function AcceptInvite() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { accessToken, loading, activeOrgId, authedCall, orgs } = useAuth();

  const queryToken = searchParams.get("token") ?? "";
  const [token, setToken] = useState(queryToken);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [accepted, setAccepted] = useState<Invitation | null>(null);

  useEffect(() => {
    if (queryToken) setToken(queryToken);
  }, [queryToken]);

  async function accept(inviteToken: string) {
    const trimmed = inviteToken.trim();
    if (!trimmed) {
      setError("Enter an invitation token to continue.");
      return;
    }
    // Not signed in: send to login and come back to this page with the token.
    if (!loading && !accessToken) {
      const back = `/invite?token=${encodeURIComponent(trimmed)}`;
      router.replace(`/login?next=${encodeURIComponent(back)}`);
      return;
    }
    setPending(true);
    setError(null);
    try {
      const res = await authedCall((t, on) =>
        api.acceptInvitation(trimmed, t, on),
      );
      setAccepted(res);
    } catch {
      setError(
        "Could not accept this invitation. It may be invalid, expired, or already used.",
      );
    } finally {
      setPending(false);
    }
  }

  function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    void accept(token);
  }

  if (accepted) {
    const orgName =
      orgs.find((o) => o.id === activeOrgId)?.name ?? "your organization";
    const scope = accepted.projectId
      ? `project ${accepted.projectId}`
      : "the whole organization";
    return (
      <Card className="glow-violet">
        <CardHeader>
          <div className="flex items-center gap-2">
            <CheckCircle2 className="h-5 w-5 text-success" />
            <CardTitle className="text-xl">Invitation accepted</CardTitle>
          </div>
          <CardDescription>
            You joined {orgName} as a{" "}
            <span className="font-medium capitalize">{accepted.role}</span> with
            access to {scope}.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button className="w-full" onClick={() => router.push("/dashboard")}>
            Go to dashboard
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="glow-violet">
      <CardHeader>
        <div className="flex items-center gap-2">
          <MailCheck className="h-5 w-5 text-primary" />
          <CardTitle className="text-xl">Accept invitation</CardTitle>
        </div>
        <CardDescription>
          {accessToken
            ? "Confirm to join the organization you were invited to."
            : "Log in to accept this invitation and join the organization."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="invite-token">Invitation token</Label>
            <Input
              id="invite-token"
              className="font-mono"
              placeholder="inv_tok_…"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
          </div>

          {error && (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </p>
          )}

          <Button type="submit" className="w-full" disabled={pending}>
            {pending && <Loader2 className="h-4 w-4 animate-spin" />}
            {accessToken ? "Accept invitation" : "Log in to accept"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

export default function InvitePage() {
  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center overflow-hidden px-4 py-12">
      <div className="absolute inset-0 grid-bg opacity-20" aria-hidden />
      <div
        className="pointer-events-none absolute left-1/2 top-[-12rem] h-[28rem] w-[28rem] -translate-x-1/2 rounded-full bg-brand-balloon opacity-20 blur-[120px]"
        aria-hidden
      />
      <Link href="/" className="relative z-10 mb-8">
        <Logo size={40} withWordmark />
      </Link>
      <div className="relative z-10 w-full max-w-md">
        <Suspense
          fallback={
            <div className="flex justify-center">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          }
        >
          <AcceptInvite />
        </Suspense>
      </div>
    </div>
  );
}
