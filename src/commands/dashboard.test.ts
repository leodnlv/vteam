import { describe, it, expect } from "vitest";
import { composeFrame } from "./dashboard.js";
import { stripAnsi } from "../ansi.js";

describe("composeFrame", () => {
  it("splits multi-line entries and appends the footer after a blank line", () => {
    expect(composeFrame(["a", "b\nc"], "footer", 24)).toEqual(["a", "b", "c", "", "footer"]);
  });

  it("clips the body to the terminal height and says how much is hidden", () => {
    const frame = composeFrame(["1", "2", "3", "4", "5", "6"], "footer", 6);
    expect(frame.map(stripAnsi)).toEqual(["1", "2", "3", "  … 3 more line(s), enlarge the terminal", "", "footer"]);
  });

  it("keeps every line when the terminal height is unknown", () => {
    expect(composeFrame(["1", "2"], "footer", undefined)).toHaveLength(4);
  });
});
