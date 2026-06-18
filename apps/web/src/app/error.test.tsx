import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import RootError from "@/app/error";
import DashboardError from "@/app/dashboard/error";

// Both error boundaries log the error on mount; silence it so the test output
// stays clean while still letting React render the component.
vi.spyOn(console, "error").mockImplementation(() => {});

afterEach(() => {
  cleanup();
});

const cases = [
  { name: "app/error.tsx", Component: RootError },
  { name: "dashboard/error.tsx", Component: DashboardError },
] as const;

describe.each(cases)("$name error boundary", ({ Component }) => {
  it("exposes the error as an alert region", () => {
    render(<Component error={new Error("boom")} reset={() => {}} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("calls reset() when Retry is clicked", () => {
    const reset = vi.fn();
    render(<Component error={new Error("boom")} reset={reset} />);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(reset).toHaveBeenCalledTimes(1);
  });
});
