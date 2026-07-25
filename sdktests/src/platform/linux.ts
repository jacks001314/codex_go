import type { PlatformCommands } from "./types.ts";

export const linuxCommands: PlatformCommands = {
  suite: "linux",
  workspaceStructuredRead: "sed -n '1,999p' ./README.txt",
  workspaceFileWrite: "printf %s SDK_FILE_WRITE_OK > ./result.txt",
  commandExitSeven: `sh -c 'printf "%s\\n" SDK_EXIT_7; exit 7'`,
};
