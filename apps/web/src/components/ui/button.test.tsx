import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Button } from "@/components/ui/button";

describe("Button", () => {
  it("renders its label", () => {
    render(<Button>Deploy</Button>);
    expect(
      screen.getByRole("button", { name: "Deploy" }),
    ).toBeInTheDocument();
  });

  it("defaults to the primary variant classes", () => {
    render(<Button>Primary</Button>);
    const btn = screen.getByRole("button", { name: "Primary" });
    expect(btn.className).toContain("bg-primary");
  });

  it("applies destructive variant classes", () => {
    render(<Button variant="destructive">Delete</Button>);
    const btn = screen.getByRole("button", { name: "Delete" });
    expect(btn.className).toContain("bg-destructive");
  });

  it("applies size classes", () => {
    render(
      <Button size="lg">Big</Button>,
    );
    const btn = screen.getByRole("button", { name: "Big" });
    expect(btn.className).toContain("h-11");
  });

  it("defaults to type=button", () => {
    render(<Button>Safe</Button>);
    const btn = screen.getByRole("button", { name: "Safe" });
    expect(btn).toHaveAttribute("type", "button");
  });

  it("forwards the disabled prop", () => {
    render(<Button disabled>Off</Button>);
    expect(screen.getByRole("button", { name: "Off" })).toBeDisabled();
  });

  it("shows a spinner and disables the button when loading", () => {
    render(<Button loading>Deploying</Button>);
    const btn = screen.getByRole("button", { name: "Deploying" });
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute("aria-busy", "true");
    // Loader2 renders an svg with the spin animation.
    const spinner = btn.querySelector("svg");
    expect(spinner).not.toBeNull();
    expect(spinner?.getAttribute("class")).toContain("animate-spin");
  });
});
