import { describe, it, expect } from "vitest";
import { cn } from "@/lib/utils";

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
