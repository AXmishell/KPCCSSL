import { describe, expect, it } from "vitest";
import { daysUntil, formatBytes, splitLines } from "./format";

describe("format helpers", () => {
  it("deduplicates SAN lines and splits commas", () => {
    expect(splitLines("example.com\nwww.example.com, example.com\n\n")).toEqual(["example.com", "www.example.com"]);
  });

  it("formats byte sizes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(5 * 1024 * 1024)).toBe("5.0 MB");
  });

  it("computes days until a timestamp", () => {
    const future = Math.floor(Date.now() / 1000) + 3 * 86400;
    expect(daysUntil(future)).toBeGreaterThanOrEqual(2);
    expect(daysUntil(null)).toBeNull();
  });
});
