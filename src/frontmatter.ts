export interface FrontmatterResult {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: Record<string, any>;
  content: string;
}

export function parse(raw: string): FrontmatterResult {
  const input = raw.startsWith("\uFEFF") ? raw.slice(1) : raw;

  if (!input.startsWith("---")) {
    return { data: {}, content: input };
  }

  const end = input.indexOf("\n---", 3);
  if (end === -1) {
    return { data: {}, content: input };
  }

  const yamlBlock = input.slice(4, end);
  const content = input.slice(end + 4);
  const data = parseYaml(yamlBlock);
  return { data, content: content.replace(/^\r?\n/, "") };
}

export function stringify(body: string, data: object): string {
  const yaml = serializeYaml(data as Record<string, unknown>);
  const normalizedBody = body.endsWith("\n") ? body : body + "\n";
  return `---\n${yaml}---\n${normalizedBody}`;
}

function parseYaml(block: string): Record<string, unknown> {
  return parseMapping(block.split("\n"), { line: 0 }, 0);
}

interface Cursor {
  line: number;
}

function indentOf(line: string): number {
  return line.length - line.trimStart().length;
}

function nextContentLine(lines: string[], from: number): number | undefined {
  for (let i = from; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (trimmed !== "" && !trimmed.startsWith("#")) return i;
  }
  return undefined;
}

function parseMapping(lines: string[], cursor: Cursor, minIndent: number): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  let next: number | undefined;
  while ((next = nextContentLine(lines, cursor.line)) !== undefined) {
    const line = lines[next];
    if (indentOf(line) < minIndent) break;
    cursor.line = next + 1;

    const trimmed = line.trim();
    const colonIdx = trimmed.indexOf(":");
    if (colonIdx === -1) continue;

    const key = trimmed.slice(0, colonIdx).trim();
    const rawValue = trimmed.slice(colonIdx + 1).trim();
    result[key] =
      rawValue === "" || rawValue === "null" || rawValue === "~"
        ? parseNested(lines, cursor, indentOf(line))
        : parseValue(rawValue);
  }
  return result;
}

function parseNested(lines: string[], cursor: Cursor, parentIndent: number): unknown {
  const first = nextContentLine(lines, cursor.line);
  if (first === undefined) return null;
  const firstIndent = indentOf(lines[first]);

  if (lines[first].trim().startsWith("- ")) {
    const items: unknown[] = [];
    let next: number | undefined = first;
    while (next !== undefined && lines[next].trim().startsWith("- ") && indentOf(lines[next]) >= parentIndent) {
      items.push(parseValue(lines[next].trim().slice(2).trim()));
      cursor.line = next + 1;
      next = nextContentLine(lines, cursor.line);
    }
    return items.length > 0 ? items : null;
  }

  if (firstIndent > parentIndent) {
    cursor.line = first;
    return parseMapping(lines, cursor, firstIndent);
  }
  return null;
}

function parseValue(raw: string): unknown {
  if (raw === "" || raw === "null" || raw === "~") return null;
  if (raw === "true") return true;
  if (raw === "false") return false;

  if (raw.startsWith("[") && raw.endsWith("]")) {
    return parseFlowArray(raw.slice(1, -1));
  }

  if (/^-?\d+$/.test(raw)) return Number(raw);
  if (/^-?\d+\.\d+$/.test(raw)) return Number(raw);

  if (
    (raw.startsWith('"') && raw.endsWith('"')) ||
    (raw.startsWith("'") && raw.endsWith("'"))
  ) {
    return raw.slice(1, -1);
  }

  return raw;
}

function parseFlowArray(inner: string): unknown[] {
  if (inner.trim() === "") return [];
  return inner.split(",").map((item) => parseValue(item.trim()));
}

function serializeYaml(data: Record<string, unknown>): string {
  const lines: string[] = [];
  for (const [key, value] of Object.entries(data)) {
    if (value === undefined) continue;
    lines.push(`${key}: ${serializeValue(value)}`);
  }
  return lines.length > 0 ? lines.join("\n") + "\n" : "";
}

function serializeValue(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (typeof value === "boolean") return String(value);
  if (typeof value === "number") return String(value);
  if (Array.isArray(value)) {
    if (value.length === 0) return "[]";
    return `[${value.map((v) => serializeScalar(v)).join(", ")}]`;
  }
  return quoteIfNeeded(String(value));
}

function serializeScalar(value: unknown): string {
  if (typeof value === "string") return quoteIfNeeded(value);
  return serializeValue(value);
}

function quoteIfNeeded(s: string): string {
  if (s === "") return '""';
  if (s === "true" || s === "false" || s === "null" || s === "~") return `"${s}"`;
  if (/^-?\d+(\.\d+)?$/.test(s)) return `"${s}"`;
  if (/[:{}\[\],&*?|>!%@`#'"]/.test(s) || s.startsWith("- ") || /^\s|\s$/.test(s)) {
    return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  }
  return s;
}
