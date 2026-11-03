import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import { color, colorEnabled, stripAnsi } from "./ansi.js";

const originalIsTTY = process.stdout.isTTY;

function setTTY(value: boolean): void {
  Object.defineProperty(process.stdout, "isTTY", { value, configurable: true });
}

beforeEach(() => {
  vi.stubEnv("NO_COLOR", "");
  vi.stubEnv("FORCE_COLOR", "");
  vi.stubEnv("TERM", "xterm-256color");
  setTTY(false);
});

afterEach(() => {
  vi.unstubAllEnvs();
  Object.defineProperty(process.stdout, "isTTY", { value: originalIsTTY, configurable: true });
});

describe("colorEnabled", () => {
  it("is false when stdout is not a TTY", () => {
    expect(colorEnabled()).toBe(false);
  });

  it("is true on a TTY with a normal terminal", () => {
    setTTY(true);

    expect(colorEnabled()).toBe(true);
  });

  it("is false on a TTY when TERM is dumb", () => {
    setTTY(true);
    vi.stubEnv("TERM", "dumb");

    expect(colorEnabled()).toBe(false);
  });

  it("is true without a TTY when FORCE_COLOR is set", () => {
    vi.stubEnv("FORCE_COLOR", "1");

    expect(colorEnabled()).toBe(true);
  });

  it("ignores FORCE_COLOR=0", () => {
    vi.stubEnv("FORCE_COLOR", "0");

    expect(colorEnabled()).toBe(false);
  });

  it("lets NO_COLOR win over FORCE_COLOR and a TTY", () => {
    setTTY(true);
    vi.stubEnv("FORCE_COLOR", "1");
    vi.stubEnv("NO_COLOR", "1");

    expect(colorEnabled()).toBe(false);
  });
});

describe("color", () => {
  it("returns plain text when color is disabled", () => {
    expect(color.red("error")).toBe("error");
  });

  it("wraps text in the matching open and close codes when enabled", () => {
    vi.stubEnv("FORCE_COLOR", "1");

    expect(color.red("error")).toBe("\x1b[31merror\x1b[39m");
    expect(color.bold("title")).toBe("\x1b[1mtitle\x1b[22m");
    expect(color.gray("note")).toBe("\x1b[90mnote\x1b[39m");
  });

  it("produces output that stripAnsi reduces back to the original text", () => {
    vi.stubEnv("FORCE_COLOR", "1");

    expect(stripAnsi(color.bold(color.cyan("hello")))).toBe("hello");
  });

  it("re-checks the environment on every call", () => {
    expect(color.green("ok")).toBe("ok");

    vi.stubEnv("FORCE_COLOR", "1");

    expect(color.green("ok")).toBe("\x1b[32mok\x1b[39m");
  });
});
