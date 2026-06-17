import { describe, it, expect } from "vitest";
import {
  cn,
  initials,
  slugify,
  buildAppFqdn,
  VORTEX_BASE_DOMAIN,
} from "@/lib/utils";

describe("cn", () => {
  it("merges multiple class strings", () => {
    expect(cn("px-2", "py-1")).toBe("px-2 py-1");
  });

  it("applies conditional classes via clsx semantics", () => {
    expect(cn("base", false && "hidden", "block")).toBe("base block");
  });

  it("de-duplicates conflicting tailwind utilities (last wins)", () => {
    expect(cn("px-2", "px-4")).toBe("px-4");
  });

  it("merges conflicting color utilities", () => {
    expect(cn("text-red-500", "text-blue-500")).toBe("text-blue-500");
  });

  it("supports array and object inputs", () => {
    expect(cn(["p-2", "m-2"], { hidden: false, flex: true })).toBe(
      "p-2 m-2 flex",
    );
  });
});

describe("initials", () => {
  it("takes the first letters of the first two words, uppercased", () => {
    expect(initials("Grace Hopper")).toBe("GH");
    expect(initials("alan turing")).toBe("AT");
  });

  it("caps at two initials for longer names", () => {
    expect(initials("Katherine Coleman Goble Johnson")).toBe("KC");
  });

  it("handles single-word names", () => {
    expect(initials("Madonna")).toBe("M");
  });

  it("collapses extra whitespace", () => {
    expect(initials("  ada   lovelace ")).toBe("AL");
  });

  it("returns an empty string for empty/blank input", () => {
    expect(initials("")).toBe("");
    expect(initials("   ")).toBe("");
  });
});

describe("slugify", () => {
  it("lowercases and hyphenates non-alphanumerics", () => {
    expect(slugify("Acme Corp")).toBe("acme-corp");
    expect(slugify("Hello, World!")).toBe("hello-world");
  });

  it("collapses runs and trims leading/trailing hyphens", () => {
    expect(slugify("  --Foo__Bar--  ")).toBe("foo-bar");
  });

  it("falls back to 'app' when nothing survives", () => {
    expect(slugify("!!!")).toBe("app");
    expect(slugify("")).toBe("app");
  });
});

describe("buildAppFqdn", () => {
  it("builds <app>.<project>.<org>.<base> with the default project", () => {
    expect(buildAppFqdn("marketing-site", "acme-corp")).toBe(
      `marketing-site.default.acme-corp.${VORTEX_BASE_DOMAIN}`,
    );
  });

  it("slugifies each segment and honors a custom project", () => {
    expect(buildAppFqdn("My App", "Acme Corp", "Platform")).toBe(
      `my-app.platform.acme-corp.${VORTEX_BASE_DOMAIN}`,
    );
  });
});
