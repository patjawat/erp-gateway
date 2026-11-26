import { describe, expect, it } from "vitest";
import { count, thaiDate, thaiTime, usageReady } from "./format";
describe("Thai display and measurement states", () => {
  it("formats date-only values in Buddhist Era without inventing a time", () => {
    expect(thaiDate("2026-11-25")).toBe("25 พ.ย. 2569");
    expect(thaiDate("")).toBe("—");
    expect(thaiDate("invalid")).toBe("—");
  });
  it("uses Bangkok 24-hour time for recorded timestamps", () => {
    expect(thaiTime(Date.parse("2026-11-25T02:42:00Z") / 1000)).toBe(
      "25 พ.ย. 2569 เวลา 09:42 น.",
    );
  });
  it("distinguishes a real zero from missing or stale usage", () => {
    expect(count(0)).toBe("0");
    expect(count(null)).toBe("—");
    expect(count(undefined)).toBe("—");
    expect(usageReady("stale")).toBe(false);
    expect(usageReady("unsupported")).toBe(false);
    expect(usageReady("partial")).toBe(true);
  });
});
