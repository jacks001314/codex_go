import type { PlatformCommands } from "./types.ts";

export const linuxCommands: PlatformCommands = {
  suite: "linux",
  workspaceStructuredRead: "sed -n '1,999p' ./README.txt",
  workspaceFileWrite: "printf %s SDK_FILE_WRITE_OK > ./result.txt",
  commandExitSeven: `sh -c 'printf "%s\\n" SDK_EXIT_7; exit 7'`,
  commandExitSevenCode: 7,
  jsExecSingle: "printf '%s\\n' JS_EXEC_OK",
  jsExecStepOne: "printf '%s\\n' STEP_1",
  jsExecStepTwo: "printf '%s\\n' STEP_2",
  jsExecRejectedWrite: "printf %s DENIED > ./should-not-exist.txt",
  jsExecInterrupt: "sleep 30; printf %s SHOULD_NOT_EXIST > ./interrupted-marker.txt",
  jsExecInterruptProbe: "if test -e ./interrupted-marker.txt; then printf '%s\\n' MARKER_EXISTS; else printf '%s\\n' MARKER_ABSENT; fi",
  jsExecParallelSlow: "sleep 0.2; printf '%s\\n' PARALLEL_SLOW",
  jsExecParallelFast: "sleep 0.02; printf '%s\\n' PARALLEL_FAST",
};
