const ESC = String.fromCharCode(27);
const ANSI_PATTERN = new RegExp(`${ESC}\\[[0-9;]*m`, "g");

export function colorEnabled(): boolean {
  if (process.env.NO_COLOR) return false;
  if (process.env.FORCE_COLOR && process.env.FORCE_COLOR !== "0") return true;
  return Boolean(process.stdout.isTTY) && process.env.TERM !== "dumb";
}

function paint(open: number, close: number): (text: string) => string {
  return (text) => (colorEnabled() ? `${ESC}[${open}m${text}${ESC}[${close}m` : text);
}

export const color = {
  bold: paint(1, 22),
  dim: paint(2, 22),
  red: paint(31, 39),
  green: paint(32, 39),
  yellow: paint(33, 39),
  blue: paint(34, 39),
  magenta: paint(35, 39),
  cyan: paint(36, 39),
  gray: paint(90, 39),
};

export function stripAnsi(text: string): string {
  return text.replace(ANSI_PATTERN, "");
}
