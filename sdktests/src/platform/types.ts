export type PlatformSuite = "linux" | "windows";

export type PlatformCommands = {
  suite: PlatformSuite;
  workspaceStructuredRead: string;
  workspaceFileWrite: string;
  commandExitSeven: string;
};
