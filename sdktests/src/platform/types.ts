export type PlatformSuite = "linux" | "windows";

export type PlatformCommands = {
  suite: PlatformSuite;
  workspaceStructuredRead: string;
  workspaceFileWrite: string;
  commandExitSeven: string;
  commandExitSevenCode: number;
  jsExecSingle: string;
  jsExecStepOne: string;
  jsExecStepTwo: string;
  jsExecRejectedWrite: string;
  jsExecInterrupt: string;
  jsExecInterruptProbe: string;
  jsExecParallelSlow: string;
  jsExecParallelFast: string;
};
