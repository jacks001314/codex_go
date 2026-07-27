import type { PlatformCommands } from "./types.ts";

export const windowsCommands: PlatformCommands = {
  suite: "windows",
  workspaceStructuredRead: "Get-Content -LiteralPath .\\README.txt",
  workspaceFileWrite: "Set-Content -LiteralPath .\\result.txt -Value SDK_FILE_WRITE_OK -NoNewline",
  commandExitSeven: `powershell -NoProfile -Command "Write-Output SDK_EXIT_7; exit 7"`,
  commandExitSevenCode: 1,
  jsExecSingle: "Write-Output JS_EXEC_OK",
  jsExecStepOne: "Write-Output STEP_1",
  jsExecStepTwo: "Write-Output STEP_2",
  jsExecRejectedWrite: "Set-Content -LiteralPath .\\should-not-exist.txt -Value DENIED -NoNewline",
  jsExecInterrupt: "Start-Sleep -Seconds 30; Set-Content -LiteralPath .\\interrupted-marker.txt -Value SHOULD_NOT_EXIST -NoNewline",
  jsExecInterruptProbe: "if (Test-Path .\\interrupted-marker.txt) { Write-Output MARKER_EXISTS } else { Write-Output MARKER_ABSENT }",
  jsExecParallelSlow: "Start-Sleep -Milliseconds 200; Write-Output PARALLEL_SLOW",
  jsExecParallelFast: "Start-Sleep -Milliseconds 20; Write-Output PARALLEL_FAST",
};
