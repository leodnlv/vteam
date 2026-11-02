import { describe, it, expect } from "vitest";
import { formatAge, formatClock, formatDuration, truncate } from "./log.js";
import { stripAnsi } from "./ansi.js";

describe("formatClock", () => {
  it("formats elapsed time as mm:ss, adding hours when needed", () => {
    expect(formatClock(0)).toBe("00:00");
    expect(formatClock(252_400)).toBe("04:12");
    expect(formatClock(3_725_000)).toBe("1:02:05");
  });
});

describe("formatDuration", () => {
  it("uses the largest two units", () => {
    expect(formatDuration(9_400)).toBe("9s");
    expect(formatDuration(252_000)).toBe("4m12s");
    expect(formatDuration(3_780_000)).toBe("1h03m");
  });
});

describe("truncate", () => {
  it("cuts long text with an ellipsis and leaves short text alone", () => {
    expect(truncate("abcdef", 4)).toBe("abc…");
    expect(truncate("abc", 4)).toBe("abc");
  });
});

describe("stripAnsi", () => {
  it("removes color escape sequences", () => {
    expect(stripAnsi("\x1b[1m\x1b[36mhi\x1b[39m\x1b[22m")).toBe("hi");
  });
});

describe("formatAge", () => {
  it.each([
    [0, "<1m"],
    [-5_000, "<1m"],
    [12 * 60_000, "12m"],
    [(3 * 60 + 42) * 60_000, "3h 42m"],
    [(26 * 60 + 5) * 60_000, "1d 2h"],
  ])("formats %d ms as %s", (ms, expected) => {
    expect(formatAge(ms)).toBe(expected);
  });
});
