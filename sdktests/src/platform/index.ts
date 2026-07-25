import { linuxCommands } from "./linux.ts";
import type { PlatformCommands, PlatformSuite } from "./types.ts";
import { windowsCommands } from "./windows.ts";

export type { PlatformSuite } from "./types.ts";

export function currentPlatformSuite(): PlatformSuite {
  if (process.platform === "linux") return "linux";
  if (process.platform === "win32") return "windows";
  throw new Error(`Unsupported live sdktests platform: ${process.platform}`);
}

export function platformCommands(): PlatformCommands {
  return currentPlatformSuite() === "windows" ? windowsCommands : linuxCommands;
}
