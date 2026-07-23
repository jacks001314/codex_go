import path from "node:path";

type MapState = {
  ids: Map<string, string>;
  paths: Map<string, string>;
};

const uuidLike = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const timestampLike = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/;

export function normalizeRecording(recording: any): any {
  const state: MapState = { ids: new Map(), paths: new Map() };
  return normalizeValue(recording, state, []);
}

function normalizeValue(value: any, state: MapState, keyPath: string[]): any {
  if (Array.isArray(value)) {
    return value.map((item, index) => normalizeValue(item, state, [...keyPath, String(index)]));
  }
  if (value && typeof value === "object") {
    const out: Record<string, any> = {};
    for (const [key, child] of Object.entries(value)) {
      if (isVolatileNumericField(key)) {
        out[key] = `<${key.toUpperCase()}>`;
      } else if (key === "usage" && child && typeof child === "object") {
        out[key] = normalizeUsage(child);
      } else {
        out[key] = normalizeValue(child, state, [...keyPath, key]);
      }
    }
    return out;
  }
  if (typeof value === "string") {
    if (uuidLike.test(value) || keyPath.at(-1)?.endsWith("_id") || keyPath.at(-1) === "id") {
      return mapped(state.ids, value, "ID");
    }
    if (timestampLike.test(value)) {
      return "<TIMESTAMP>";
    }
    if (looksLikePath(value)) {
      return mapped(state.paths, path.normalize(value), "PATH");
    }
  }
  return value;
}

function normalizeUsage(usage: any): any {
  const out: Record<string, string> = {};
  for (const key of Object.keys(usage).sort()) {
    out[key] = "<TOKEN_COUNT>";
  }
  return out;
}

function mapped(map: Map<string, string>, value: string, prefix: string): string {
  const existing = map.get(value);
  if (existing) {
    return existing;
  }
  const next = `<${prefix}_${map.size + 1}>`;
  map.set(value, next);
  return next;
}

function isVolatileNumericField(key: string): boolean {
  return ["durationMs", "duration_ms", "elapsed_ms", "exitCode"].includes(key);
}

function looksLikePath(value: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(value) || value.includes("\\sdktests\\.tmp\\");
}
