import type { PlatformCommands } from "./types.ts";

export const windowsCommands: PlatformCommands = {
  suite: "windows",
  workspaceStructuredRead: "Get-Content -LiteralPath .\\README.txt",
  workspaceFileWrite: "Set-Content -LiteralPath .\\result.txt -Value SDK_FILE_WRITE_OK -NoNewline",
  commandExitSeven: `powershell -NoProfile -Command "Write-Output SDK_EXIT_7; exit 7"`,
};
