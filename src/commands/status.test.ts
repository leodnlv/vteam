import { describe, it, expect } from "vitest";
import { formatDuration } from "./status.js";

describe("formatDuration", () => {
  it.each([
    [0, "<1m"],
    [-5_000, "<1m"],
    [12 * 60_000, "12m"],
    [(3 * 60 + 42) * 60_000, "3h 42m"],
    [(26 * 60 + 5) * 60_000, "1d 2h"],
  ])("formats %d ms as %s", (ms, expected) => {
    expect(formatDuration(ms)).toBe(expected);
  });
});
