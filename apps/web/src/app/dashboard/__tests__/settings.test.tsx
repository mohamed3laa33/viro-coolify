import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
  within,
} from "@testing-library/react";
import {
  ApiError,
  type BillingResponse,
  type Invitation,
  type Member,
  type PricingComponent,
} from "@/lib/api";
import { __clearResourceCache } from "@/lib/use-resource";
import SettingsPage from "@/app/dashboard/settings/page";

// authedCall invokes the supplied fn with a fake token + no-op onUnauthorized,
// mirroring the real provider so the page's `api.*` calls run through unchanged.
const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u_1", email: "owner@acme.dev", isAdmin: false },
    orgs: [{ id: "org_1", name: "Acme", slug: "acme" }],
    activeOrgId: "org_1",
    authedCall,
  }),
}));

// Force production-shaped behavior: `isDemoMode()` false means a real failure
// must render an honest `error` Notice — never a "queued"/"demo mode" message.
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

// Stub the demo-data hook so it returns its `empty` fallback synchronously
// (no dynamic mock import, no async state swap). This matches the production
// path where `useDemoData` never loads fabricated data. The real hook pins the
// `empty` value in `useState` on mount, so the reference is STABLE across
// re-renders; mirror that here (return the first `empty` we saw) — otherwise an
// inline `[]` fallback would change identity every render and retrigger the
// `useResource` effect (deps include the fallback), inflating fetch counts.
vi.mock("@/lib/demo-data", async () => {
  const { useRef } = await import("react");
  return {
    useDemoData: <T,>(_select: unknown, empty: T): T => {
      const ref = useRef(empty);
      return ref.current;
    },
    demoEnabled: () => false,
  };
});

const listMembers = vi.fn();
const listInvitations = vi.fn();
const listProjects = vi.fn();
const updateOrg = vi.fn();
const updateMember = vi.fn();
const removeMember = vi.fn();
const invite = vi.fn();
const revokeInvitation = vi.fn();
const getPlans = vi.fn();
const getPricing = vi.fn();
const getBilling = vi.fn();
const subscribe = vi.fn();
const getSettings = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listMembers: (...a: unknown[]) => listMembers(...a),
      listInvitations: (...a: unknown[]) => listInvitations(...a),
      listProjects: (...a: unknown[]) => listProjects(...a),
      updateOrg: (...a: unknown[]) => updateOrg(...a),
      updateMember: (...a: unknown[]) => updateMember(...a),
      removeMember: (...a: unknown[]) => removeMember(...a),
      invite: (...a: unknown[]) => invite(...a),
      revokeInvitation: (...a: unknown[]) => revokeInvitation(...a),
      getPlans: (...a: unknown[]) => getPlans(...a),
      getPricing: (...a: unknown[]) => getPricing(...a),
      getBilling: (...a: unknown[]) => getBilling(...a),
      subscribe: (...a: unknown[]) => subscribe(...a),
      getSettings: (...a: unknown[]) => getSettings(...a),
    },
  };
});

const MEMBER: Member = {
  userId: "u_2",
  email: "teammate@acme.dev",
  name: "Tess Teammate",
  role: "member",
};

const INVITE: Invitation = {
  id: "inv_1",
  email: "invitee@acme.dev",
  role: "member",
  projectId: null,
  token: "tok_abc",
  status: "pending",
  createdAt: "2026-06-01T00:00:00Z",
};

const PRICING: PricingComponent[] = [
  {
    key: "cpu",
    name: "vCPU",
    unit: "core-hour",
    pricePerHour: 4.2,
    currency: "usd",
    active: true,
    sortOrder: 0,
  },
  {
    key: "mem",
    name: "Memory",
    unit: "GB-hour",
    pricePerHour: 1.1,
    currency: "usd",
    active: true,
    sortOrder: 1,
  },
  {
    key: "legacy",
    name: "Retired meter",
    unit: "hour",
    pricePerHour: 9.9,
    currency: "usd",
    active: false,
    sortOrder: 2,
  },
];

const EMPTY_BILLING: BillingResponse = {
  subscription: null,
  plan: null,
  usage: {},
};

function setTeamDefaults() {
  listMembers.mockReset().mockResolvedValue({ data: [MEMBER] });
  listInvitations.mockReset().mockResolvedValue({ data: [INVITE] });
  listProjects.mockReset().mockResolvedValue({ data: [] });
  updateMember.mockReset().mockResolvedValue(MEMBER);
  removeMember.mockReset().mockResolvedValue(undefined);
  invite.mockReset().mockResolvedValue(INVITE);
  revokeInvitation.mockReset().mockResolvedValue(undefined);
}

function setBillingDefaults() {
  getPlans.mockReset().mockResolvedValue({ data: [], provider: "stripe" });
  getPricing.mockReset().mockResolvedValue({ data: PRICING });
  getBilling.mockReset().mockResolvedValue(EMPTY_BILLING);
  subscribe.mockReset().mockResolvedValue({
    subscription: { ...EMPTY_BILLING, status: "active" },
  });
}

beforeEach(() => {
  __clearResourceCache();
  authedCall.mockClear();
  updateOrg.mockReset().mockResolvedValue({ id: "org_1", name: "Acme" });
  getSettings.mockReset().mockResolvedValue({});
  setTeamDefaults();
  setBillingDefaults();
});

afterEach(() => cleanup());

// Click the "Team" tab and wait for the roster to populate.
async function openTeamTab() {
  fireEvent.click(screen.getByRole("tab", { name: "Team" }));
  await screen.findByText(MEMBER.name);
}

// Click the "Billing" tab and wait for the pricing call to fire.
async function openBillingTab() {
  fireEvent.click(screen.getByRole("tab", { name: "Billing" }));
  await waitFor(() => expect(getPricing).toHaveBeenCalled());
}

describe("Settings — honest prod failures (invariant #6)", () => {
  it("onInvite failure (network) renders an honest error Notice, never 'queued'", async () => {
    invite.mockRejectedValue(new TypeError("Failed to fetch"));
    render(<SettingsPage />);
    await openTeamTab();

    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "new@acme.dev" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send invite" }));

    // The error Notice (role=alert) shows the network fallback verbatim.
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't send the invitation.");
    // Never any fake-success / queued wording.
    expect(alert.textContent ?? "").not.toMatch(/queued/i);
    expect(screen.queryByText(/queued/i)).not.toBeInTheDocument();
    // No success Notice was rendered.
    expect(screen.queryByText(/Invitation sent/i)).not.toBeInTheDocument();
  });

  it("onInvite failure (backend 4xx) surfaces the server message, not a fallback", async () => {
    invite.mockRejectedValue(new ApiError("Seat limit reached", 409));
    render(<SettingsPage />);
    await openTeamTab();

    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "new@acme.dev" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send invite" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Seat limit reached");
    expect(alert.textContent ?? "").not.toMatch(/queued/i);
    expect(alert.textContent ?? "").not.toMatch(/demo mode/i);
  });

  it("subscribe failure (network) renders an honest error Notice, never 'queued'", async () => {
    getPlans.mockResolvedValue({
      data: [
        {
          id: "plan_pro",
          name: "Pro",
          description: "",
          priceCents: 2000,
          currency: "usd",
          includedHours: 100,
          overagePerHourCents: 5,
        },
      ],
      provider: "stripe",
    });
    subscribe.mockRejectedValue(new TypeError("Failed to fetch"));
    render(<SettingsPage />);
    await openBillingTab();

    fireEvent.click(
      await screen.findByRole("button", { name: /Switch to Pro/i }),
    );

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't update the subscription.");
    expect(alert.textContent ?? "").not.toMatch(/queued/i);
    expect(screen.queryByText(/queued/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Subscription updated/i)).not.toBeInTheDocument();
  });

  it("subscribe failure (backend 4xx) surfaces the server message", async () => {
    getPlans.mockResolvedValue({
      data: [
        {
          id: "plan_pro",
          name: "Pro",
          description: "",
          priceCents: 2000,
          currency: "usd",
          includedHours: 100,
          overagePerHourCents: 5,
        },
      ],
      provider: "stripe",
    });
    subscribe.mockRejectedValue(new ApiError("Payment method required", 402));
    render(<SettingsPage />);
    await openBillingTab();

    fireEvent.click(
      await screen.findByRole("button", { name: /Switch to Pro/i }),
    );

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Payment method required");
    expect(alert.textContent ?? "").not.toMatch(/queued/i);
  });
});

describe("Settings — Team mutations behind ConfirmDialog + refetch", () => {
  it("changes a member's role via api.updateMember and refetches the roster", async () => {
    render(<SettingsPage />);
    await openTeamTab();

    expect(listMembers).toHaveBeenCalledTimes(1);

    fireEvent.change(screen.getByLabelText(`Role for ${MEMBER.name}`), {
      target: { value: "admin" },
    });

    await waitFor(() => expect(updateMember).toHaveBeenCalledTimes(1));
    const [orgId, userId, input] = updateMember.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(userId).toBe(MEMBER.userId);
    expect(input).toEqual({ role: "admin" });

    // Refetch fires a second listMembers call.
    await waitFor(() => expect(listMembers).toHaveBeenCalledTimes(2));
  });

  it("removes a member only after confirming, then refetches", async () => {
    render(<SettingsPage />);
    await openTeamTab();

    // Opening the confirm dialog does not call the API yet.
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Remove member?");
    expect(removeMember).not.toHaveBeenCalled();

    // Confirm inside the dialog (the confirmLabel button).
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(removeMember).toHaveBeenCalledTimes(1));
    const [orgId, userId] = removeMember.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(userId).toBe(MEMBER.userId);

    await waitFor(() => expect(listMembers).toHaveBeenCalledTimes(2));
  });

  it("does not remove a member when the confirm dialog is cancelled", async () => {
    render(<SettingsPage />);
    await openTeamTab();

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(removeMember).not.toHaveBeenCalled();
    // No refetch beyond the initial load.
    expect(listMembers).toHaveBeenCalledTimes(1);
  });

  it("revokes an invitation only after confirming, then refetches", async () => {
    render(<SettingsPage />);
    await openTeamTab();

    expect(listInvitations).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Revoke invitation?");
    expect(revokeInvitation).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokeInvitation).toHaveBeenCalledTimes(1));
    const [orgId, inviteId] = revokeInvitation.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(inviteId).toBe(INVITE.id);

    await waitFor(() => expect(listInvitations).toHaveBeenCalledTimes(2));
  });
});

describe("Settings — Billing hourly rates from api.getPricing (invariant #1)", () => {
  it("renders only active hourly rates, sorted, from the API", async () => {
    render(<SettingsPage />);
    await openBillingTab();

    expect(await screen.findByText("Hourly rates")).toBeInTheDocument();

    // Active components are shown with their formatted per-unit rate.
    expect(screen.getByText("vCPU")).toBeInTheDocument();
    expect(screen.getByText("$0.042 / core-hour")).toBeInTheDocument();
    expect(screen.getByText("Memory")).toBeInTheDocument();
    expect(screen.getByText("$0.011 / GB-hour")).toBeInTheDocument();

    // Inactive components are filtered out — nothing hardcoded surfaces.
    expect(screen.queryByText("Retired meter")).not.toBeInTheDocument();
  });

  it("omits the hourly-rates card when the API returns no active components", async () => {
    getPricing.mockResolvedValue({ data: [] as PricingComponent[] });
    render(<SettingsPage />);
    await openBillingTab();

    await waitFor(() => expect(getBilling).toHaveBeenCalled());
    expect(screen.queryByText("Hourly rates")).not.toBeInTheDocument();
  });
});
