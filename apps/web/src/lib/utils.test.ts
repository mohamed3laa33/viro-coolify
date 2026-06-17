import { describe, it, expect } from "vitest";
import {
  cn,
  initials,
  slugify,
  buildAppFqdn,
  safeNextPath,
  DEFAULT_NEXT_PATH,
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
  it("derives two uppercase initials from a full name", () => {
    expect(initials("Ada Lovelace")).toBe("AL");
  });

  it("uppercases a single-word name's first letter", () => {
    expect(initials("ada")).toBe("A");
  });

  it("limits output to the first two words", () => {
    expect(initials("Grace Brewster Murray Hopper")).toBe("GB");
  });

  it("collapses extra whitespace between words", () => {
    expect(initials("  Ada   Lovelace  ")).toBe("AL");
  });

  it("returns an empty string for blank input", () => {
    expect(initials("")).toBe("");
    expect(initials("   ")).toBe("");
  });
});

describe("slugify", () => {
  it("lowercases and hyphenates a value", () => {
    expect(slugify("My Cool App")).toBe("my-cool-app");
  });

  it("collapses runs of non-alphanumerics to a single hyphen", () => {
    expect(slugify("a__b  c--d")).toBe("a-b-c-d");
  });

  it("trims leading and trailing hyphens", () => {
    expect(slugify("--Hello, World!--")).toBe("hello-world");
  });

  it("falls back to 'app' when nothing usable remains", () => {
    expect(slugify("!!!")).toBe("app");
    expect(slugify("")).toBe("app");
  });
});

describe("buildAppFqdn", () => {
  it("builds <app>.<project>.<org>.vortex.v60ai.com", () => {
    expect(buildAppFqdn("My App", "Acme Inc", "Prod Env")).toBe(
      "my-app.prod-env.acme-inc.vortex.v60ai.com",
    );
  });

  it("defaults the project segment to 'default'", () => {
    expect(buildAppFqdn("api", "acme")).toBe(
      "api.default.acme.vortex.v60ai.com",
    );
  });

  it("slugifies each segment", () => {
    expect(buildAppFqdn("Web UI", "Big Co!", "Staging 2")).toBe(
      "web-ui.staging-2.big-co.vortex.v60ai.com",
    );
  });
});

describe("safeNextPath", () => {
  it("returns a same-origin absolute path unchanged", () => {
    expect(safeNextPath("/dashboard/apps")).toBe("/dashboard/apps");
  });

  it("preserves query strings and fragments on safe paths", () => {
    expect(safeNextPath("/dashboard?tab=apps#top")).toBe(
      "/dashboard?tab=apps#top",
    );
  });

  it("falls back to the default for null", () => {
    expect(safeNextPath(null)).toBe(DEFAULT_NEXT_PATH);
    expect(safeNextPath(null)).toBe("/dashboard");
  });

  it("falls back for an empty string", () => {
    expect(safeNextPath("")).toBe("/dashboard");
  });

  it("rejects relative paths that don't start with '/'", () => {
    expect(safeNextPath("dashboard")).toBe("/dashboard");
  });

  it("blocks protocol-relative '//evil.com' bypass", () => {
    expect(safeNextPath("//evil.com")).toBe("/dashboard");
    expect(safeNextPath("//evil.com/path")).toBe("/dashboard");
  });

  it("blocks absolute 'http://evil.com' URLs", () => {
    expect(safeNextPath("http://evil.com")).toBe("/dashboard");
    expect(safeNextPath("https://evil.com/x")).toBe("/dashboard");
  });

  it("blocks 'javascript:' and other scheme-bearing values", () => {
    expect(safeNextPath("javascript:alert(1)")).toBe("/dashboard");
    expect(safeNextPath("/path:with:colon")).toBe("/dashboard");
  });
});
