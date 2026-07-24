export type Scenario = {
  name: string;
  description: string;
  optIn?: boolean;
  timeoutMs: number;
  workingDirectoryMode?: "fixture" | "missing";
  abortBeforeRun?: boolean;
  additionalDirectoryMode?: "none" | "fixture";
  localImageFixture?: string;
  concurrentResumeAfterFirstTurn?: boolean;
  threadOptions: {
    sandboxMode: "read-only" | "workspace-write" | "danger-full-access";
    skipGitRepoCheck: boolean;
    approvalPolicy: "never" | "on-request" | "on-failure" | "untrusted";
    networkAccessEnabled: boolean;
    webSearchMode: "disabled" | "cached" | "live";
    additionalDirectories?: string[];
  };
  turns: {
    prompt: string;
    outputSchema?: Record<string, unknown>;
    resume?: boolean;
    includeLocalImage?: boolean;
    abortAfterEventType?: string;
    continueAfterError?: boolean;
    timeoutMs?: number;
    threadOptions?: Partial<Scenario["threadOptions"]>;
    workingDirectoryMode?: "fixture" | "missing";
    additionalDirectoryMode?: "none" | "fixture";
  }[];
  expected: {
    outcome?: "success" | "failure";
    errorPattern?: string;
    terminal: "turn.completed";
    minAgentMessages: number;
    requireUsage: boolean;
    expectedTurns: number;
    exactAgentMessages?: string[];
    structuredAgentMessages?: unknown[];
    agentMessageContracts?: ({ exact: string } | { structured: unknown })[];
    requiredCompletedItemTypes?: string[];
    commandExecutions?: {
      status: string;
      exitCode: number;
      output?: string;
      outputPattern?: string;
    }[];
    fileChanges?: {
      status: string;
      stderrPattern?: string;
    }[];
    commandOutputComparison?: "exact" | "status-exit-code";
    eventSequenceComparison?: "strict" | "semantic-tools";
    agentMessageComparison?: "strict" | "final-per-turn";
    compareWorkspacePaths?: string[];
    forbiddenCompletedItemTypes?: string[];
    requireStableThreadId?: boolean;
    workspaceMutation: "none" | "required";
    workspaceChanges?: {
      path: string;
      change: "added" | "removed" | "modified";
      hash?: string;
    }[];
    workspaceRequiredPaths?: string[];
  };
};

export const scenarios: Scenario[] = [
  {
    name: "resume-real-coding-recovery",
    description: "Completes a multi-file refactor, resumes it in a fresh CLI process, recovers from a failed patch, and runs tests.",
    timeoutMs: 240000,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      {
        prompt: "Run exactly one shell command to read legacy_math.py, service.py, test_service.py, and obsolete.txt: Get-Content -LiteralPath .\\legacy_math.py, .\\service.py, .\\test_service.py, .\\obsolete.txt. Then call apply_patch exactly once with the exact patch below. Do not run tests in this turn.\n*** Begin Patch\n*** Update File: legacy_math.py\n*** Move to: math_utils.py\n@@\n def add(left, right):\n     return left + right\n*** Update File: service.py\n@@\n-from legacy_math import add\n+from math_utils import add\n*** Delete File: obsolete.txt\n*** End Patch\nAfter the patch succeeds, reply with exactly CORE_LOOP_TURN1_OK.",
      },
      {
        resume: true,
        prompt: "This is a resumed turn in a fresh CLI process. First call apply_patch exactly once with this exact patch, which must fail because its context is absent; do not retry it:\n*** Begin Patch\n*** Update File: service.py\n@@\n-MISSING_CONTEXT\n+SHOULD_NOT_APPEAR\n*** End Patch\nAfter that failure, call apply_patch exactly once more with the exact patch below to fix the implementation and add its regression test:\n*** Begin Patch\n*** Update File: service.py\n@@\n def total(values):\n+    if not isinstance(values, list):\n+        raise TypeError(\"values must be a list\")\n     result = 0\n*** Update File: test_service.py\n@@\n     def test_empty(self):\n         self.assertEqual(total([]), 0)\n+\n+    def test_rejects_non_list(self):\n+        with self.assertRaisesRegex(TypeError, \"values must be a list\"):\n+            total((1, 2))\n*** End Patch\nThen run exactly one shell command: python -m unittest -v test_service.py. After tests pass, reply with exactly CORE_LOOP_RECOVERY_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["CORE_LOOP_TURN1_OK", "CORE_LOOP_RECOVERY_OK"],
      requiredCompletedItemTypes: ["command_execution", "file_change", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0 }, { status: "completed", exitCode: 0 }],
      commandOutputComparison: "status-exit-code",
      fileChanges: [{ status: "completed" }, { status: "completed" }],
      requireStableThreadId: true, eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      workspaceRequiredPaths: ["math_utils.py", "service.py", "test_service.py"],
      compareWorkspacePaths: ["legacy_math.py", "math_utils.py", "service.py", "test_service.py", "obsolete.txt"],
    },
  },
  {
    name: "apply-patch-absolute-path-success",
    description: "Uses absolute in-workspace paths for update, add, move, and delete like Rust apply_patch.",
    timeoutMs: 240000,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [{ prompt: "You must call apply_patch exactly once using the exact patch below, even though its paths are absolute. Do not explain, do not skip the tool, do not retry, and do not run shell commands.\n*** Begin Patch\n*** Update File: {{WORKSPACE}}/state.txt\n@@\n-BASE\n+ABSOLUTE_UPDATED\n*** Add File: {{WORKSPACE}}/absolute-added.txt\n+ABSOLUTE_ADDED\n*** Update File: {{WORKSPACE}}/guard.txt\n*** Move to: {{WORKSPACE}}/absolute-moved.txt\n@@\n-UNCHANGED\n+ABSOLUTE_MOVED\n*** End Patch\nAfter the tool succeeds, reply with exactly ABSOLUTE_PATH_SUCCESS." }],
    expected: { terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["ABSOLUTE_PATH_SUCCESS"], requiredCompletedItemTypes: ["file_change", "agent_message"],
      fileChanges: [{ status: "completed" }], forbiddenCompletedItemTypes: ["command_execution"],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "required",
      workspaceRequiredPaths: ["state.txt", "absolute-added.txt", "absolute-moved.txt"],
      compareWorkspacePaths: ["state.txt", "guard.txt", "absolute-added.txt", "absolute-moved.txt"] },
  },
  {
    name: "resume-concurrent-nonoverlap",
    description: "Runs two simultaneous SDK resume calls against one persisted thread and applies non-overlapping patches.",
    timeoutMs: 180000,
    concurrentResumeAfterFirstTurn: true,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      { prompt: "Remember CONCURRENT_ROOT and reply with exactly CONCURRENT_ROOT_OK." },
      { prompt: "Resume branch A. Use apply_patch exactly once to add branch-a.txt containing exactly A_OK. Do not touch other files or run commands. Reply exactly CONCURRENT_A_OK.", resume: true },
      { prompt: "Resume branch B. Use apply_patch exactly once to add branch-b.txt containing exactly B_OK. Do not touch other files or run commands. Reply exactly CONCURRENT_B_OK.", resume: true },
    ],
    expected: { terminal: "turn.completed", minAgentMessages: 3, requireUsage: true, expectedTurns: 3,
      exactAgentMessages: ["CONCURRENT_ROOT_OK", "CONCURRENT_A_OK", "CONCURRENT_B_OK"], requiredCompletedItemTypes: ["file_change", "agent_message"],
      fileChanges: [{ status: "completed" }, { status: "completed" }], requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "required",
      workspaceRequiredPaths: ["branch-a.txt", "branch-b.txt"], compareWorkspacePaths: ["branch-a.txt", "branch-b.txt"] },
  },
  {
    name: "resume-long-context-tools",
    description: "Carries exact facts through five cross-process resumes while mixing reads, patches, and structured output.",
    timeoutMs: 180000,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      { prompt: "Remember exactly ALPHA_7K and reply with exactly LONG_TURN1_OK." },
      { prompt: "Resume. Run exactly one shell command: Get-Content -LiteralPath .\\state.txt -Raw. Then reply with exactly LONG_TURN2_OK.", resume: true },
      { prompt: "Resume. Use apply_patch exactly once to change state.txt from BASE to LONG_CONTEXT. Do not run shell commands. Then reply with exactly LONG_TURN3_OK.", resume: true },
      { prompt: "Resume. Run exactly one shell command: Get-Content -LiteralPath .\\state.txt -Raw. Then reply with exactly ALPHA_7K|LONG_CONTEXT.", resume: true },
      { prompt: "Resume. Do not use tools. Return the remembered token and current state as JSON.", resume: true, outputSchema: { type: "object", properties: { token: { const: "ALPHA_7K" }, state: { const: "LONG_CONTEXT" } }, required: ["token", "state"], additionalProperties: false } },
    ],
    expected: { terminal: "turn.completed", minAgentMessages: 5, requireUsage: true, expectedTurns: 5,
      agentMessageContracts: [{ exact: "LONG_TURN1_OK" }, { exact: "LONG_TURN2_OK" }, { exact: "LONG_TURN3_OK" }, { exact: "ALPHA_7K|LONG_CONTEXT" }, { structured: { token: "ALPHA_7K", state: "LONG_CONTEXT" } }],
      requiredCompletedItemTypes: ["command_execution", "file_change", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "BASE" }, { status: "completed", exitCode: 0, output: "LONG_CONTEXT" }],
      fileChanges: [{ status: "completed" }], requireStableThreadId: true, eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required", compareWorkspacePaths: ["state.txt", "guard.txt"] },
  },
  {
    name: "resume-command-timeout-recovery",
    description: "Aborts a long command and verifies a later resumed turn remains usable.",
    timeoutMs: 120000,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      { prompt: "Run exactly one shell command: Start-Sleep -Seconds 30. Do nothing else.", timeoutMs: 1500, continueAfterError: true },
      { prompt: "Resume after the timeout. Run exactly one shell command: Write-Output RECOVERED. Then reply with exactly COMMAND_TIMEOUT_RECOVERED.", resume: true },
    ],
    expected: { terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["COMMAND_TIMEOUT_RECOVERED"], requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "RECOVERED" }], commandOutputComparison: "status-exit-code",
      requireStableThreadId: true, eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "none" },
  },
  {
    name: "resume-apply-patch-parse-recovery",
    description: "Recovers after malformed apply_patch input without replaying it.",
    timeoutMs: 180000,
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      { prompt: "Call apply_patch exactly once with this malformed input and do not retry: *** Begin Patch\\n*** Update File: state.txt\\nBROKEN\\n*** End Patch. Then reply with exactly PARSE_FAILURE_SEEN." },
      { prompt: "Resume. Use apply_patch exactly once to change state.txt from BASE to PARSE_RECOVERED. Do not run shell commands. Reply with exactly PARSE_RECOVERY_OK.", resume: true },
    ],
    expected: { terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["PARSE_FAILURE_SEEN", "PARSE_RECOVERY_OK"], requiredCompletedItemTypes: ["file_change", "agent_message"],
      requireStableThreadId: true, eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required", compareWorkspacePaths: ["state.txt", "guard.txt"] },
  },
  {
    name: "resume-config-change",
    description: "Measures whether resume uses newly supplied sandbox and additional-directory options.",
    timeoutMs: 180000,
    additionalDirectoryMode: "fixture",
    threadOptions: { sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" },
    turns: [
      { prompt: "Reply with exactly CONFIG_CHANGE_TURN1_OK. Do not use tools." },
      { prompt: "Resume with changed options. Run exactly one shell command: Get-Content -LiteralPath ..\\additional\\outside.txt -Raw. Then reply with exactly CONFIG_CHANGE_TURN2_OK.", resume: true,
        threadOptions: { sandboxMode: "read-only" }, additionalDirectoryMode: "fixture" },
    ],
    expected: { terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["CONFIG_CHANGE_TURN1_OK", "CONFIG_CHANGE_TURN2_OK"], requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "SDK_ADDITIONAL_DIRECTORY_FIXTURE" }],
      requireStableThreadId: true, eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "none" },
  },
  {
    name: "resume-tool-interrupted-command",
    description: "Aborts a running command, resumes the thread, and verifies the interrupted command did not finish later.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Run exactly one shell command: Start-Sleep -Seconds 30; Set-Content -LiteralPath .\\interrupted-marker.txt -Value SHOULD_NOT_EXIST -NoNewline. Do not perform any other action.",
        abortAfterEventType: "item.started",
        continueAfterError: true,
      },
      {
        prompt: "This is a resumed turn after interruption. Run exactly one shell command: if (Test-Path .\\interrupted-marker.txt) { Write-Output MARKER_EXISTS } else { Write-Output MARKER_ABSENT }. Do not modify files. Then reply with exactly INTERRUPT_RESUME_OK.",
        resume: true,
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["INTERRUPT_RESUME_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0 }],
      commandOutputComparison: "status-exit-code", requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      compareWorkspacePaths: ["interrupted-marker.txt"],
    },
  },
  {
    name: "resume-tool-repeat-idempotent",
    description: "Resumes the same thread twice and verifies the original file change is never replayed.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      { prompt: "Use apply_patch exactly once to change state.txt from BASE to ONCE. Do not run shell commands. Then reply with exactly IDEMPOTENT_TURN1_OK." },
      { prompt: "This is the first resumed turn. Do not modify any file. Run exactly one shell command: Get-Content -LiteralPath .\\state.txt -Raw. Then reply with exactly IDEMPOTENT_TURN2_OK.", resume: true },
      { prompt: "This is the second resumed turn. Do not modify any file. Run exactly one shell command: Get-Content -LiteralPath .\\state.txt -Raw. Then reply with exactly IDEMPOTENT_TURN3_OK.", resume: true },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 3, requireUsage: true, expectedTurns: 3,
      exactAgentMessages: ["IDEMPOTENT_TURN1_OK", "IDEMPOTENT_TURN2_OK", "IDEMPOTENT_TURN3_OK"],
      requiredCompletedItemTypes: ["file_change", "command_execution", "agent_message"],
      fileChanges: [{ status: "completed" }],
      commandExecutions: [
        { status: "completed", exitCode: 0, output: "ONCE" },
        { status: "completed", exitCode: 0, output: "ONCE" },
      ],
      requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required", workspaceRequiredPaths: ["state.txt", "guard.txt"],
      compareWorkspacePaths: ["state.txt", "guard.txt"],
    },
  },
  {
    name: "resume-tool-config-mapping",
    description: "Verifies working directory and additional-directory access remain effective after resume.",
    timeoutMs: 240000,
    additionalDirectoryMode: "fixture",
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      { prompt: "Run exactly one shell command: Write-Output (Split-Path -Leaf (Get-Location)); Get-Content -LiteralPath .\\state.txt -Raw. Do not modify files. Then reply with exactly CONFIG_TURN1_OK." },
      { prompt: "This is a resumed turn. Run exactly one shell command: Get-Content -LiteralPath ..\\additional\\outside.txt -Raw. Do not modify files. Then reply with exactly CONFIG_TURN2_OK.", resume: true },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["CONFIG_TURN1_OK", "CONFIG_TURN2_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      forbiddenCompletedItemTypes: ["file_change"],
      commandExecutions: [
        { status: "completed", exitCode: 0, outputPattern: "workspace[\\s\\S]*BASE" },
        { status: "completed", exitCode: 0, output: "SDK_ADDITIONAL_DIRECTORY_FIXTURE" },
      ],
      commandOutputComparison: "status-exit-code", requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "none",
    },
  },
  {
    name: "resume-tool-success-continue",
    description: "Resumes after a successful patch and performs a second distinct patch without replaying the first.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Use apply_patch exactly once to change state.txt from BASE to TURN1. Do not modify guard.txt and do not run shell commands. Then reply with exactly RESUME_TOOL_TURN1_OK.",
      },
      {
        prompt: "This is a resumed turn. Use apply_patch exactly once to change state.txt from TURN1 to TURN2. Do not repeat the previous patch, do not modify guard.txt, and do not run shell commands. Then reply with exactly RESUME_TOOL_TURN2_OK.",
        resume: true,
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["RESUME_TOOL_TURN1_OK", "RESUME_TOOL_TURN2_OK"],
      requiredCompletedItemTypes: ["file_change", "agent_message"],
      fileChanges: [{ status: "completed" }, { status: "completed" }],
      forbiddenCompletedItemTypes: ["command_execution"],
      requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      workspaceRequiredPaths: ["state.txt", "guard.txt"],
      compareWorkspacePaths: ["state.txt", "guard.txt"],
    },
  },
  {
    name: "resume-tool-failure-no-replay",
    description: "Resumes after a failed patch and confirms the failed tool is not replayed before a later successful patch.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Update File: state.txt\n@@\n-MISSING\n+BROKEN\n*** End Patch. After it fails, reply with exactly RESUME_FAILURE_TURN1_OK.",
      },
      {
        prompt: "This is a resumed turn. Do not replay the failed patch. Use apply_patch exactly once to change state.txt from BASE to RECOVERED. Do not modify guard.txt and do not run shell commands. Then reply with exactly RESUME_FAILURE_TURN2_OK.",
        resume: true,
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["RESUME_FAILURE_TURN1_OK", "RESUME_FAILURE_TURN2_OK"],
      requiredCompletedItemTypes: ["file_change", "agent_message"],
      forbiddenCompletedItemTypes: ["command_execution"],
      requireStableThreadId: true,
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      workspaceRequiredPaths: ["state.txt", "guard.txt"],
      compareWorkspacePaths: ["state.txt", "guard.txt"],
    },
  },
  {
    name: "real-coding-multifile-refactor",
    description: "Moves a module, updates its consumer, removes an obsolete file, and runs tests.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Inspect legacy_math.py, service.py, obsolete.txt, and test_service.py with exactly one shell command. Then use exactly one apply_patch call to move legacy_math.py to math_utils.py without changing its function, update service.py to import add from math_utils, add a type guard at the start of total(values) that raises TypeError('values must be a list') when values is not a list, and delete obsolete.txt. Do not modify test_service.py. Finally run exactly one shell command: python -m unittest -v test_service.py. After tests pass, reply with exactly MULTIFILE_REFACTOR_OK.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["MULTIFILE_REFACTOR_OK"],
      requiredCompletedItemTypes: ["file_change", "command_execution", "agent_message"],
      fileChanges: [{ status: "completed" }],
      commandExecutions: [{ status: "completed", exitCode: 0 }, { status: "completed", exitCode: 0 }],
      commandOutputComparison: "status-exit-code",
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      workspaceRequiredPaths: ["math_utils.py", "service.py", "test_service.py"],
      compareWorkspacePaths: ["legacy_math.py", "math_utils.py", "service.py", "obsolete.txt", "test_service.py"],
    },
  },
  {
    name: "apply-patch-multifile-atomic-failure",
    description: "Checks whether a later failing hunk leaves earlier file changes behind.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Add File: should-not-remain.txt\n+temporary\n*** Update File: README.txt\n@@\n-context that is definitely absent\n+replacement\n*** End Patch. After the tool returns, reply with exactly ATOMIC_FAILURE_OBSERVED.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["ATOMIC_FAILURE_OBSERVED"], requiredCompletedItemTypes: ["agent_message"],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "none",
    },
  },
  {
    name: "apply-patch-absolute-path-failure",
    description: "Rejects an absolute apply_patch path and preserves the workspace.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Add File: C:\\sdk-absolute-path.txt\n+must not be written\n*** End Patch. After the tool rejects it, reply with exactly ABSOLUTE_PATH_REJECTED.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["ABSOLUTE_PATH_REJECTED"],
      requiredCompletedItemTypes: ["file_change", "agent_message"],
      fileChanges: [{ status: "failed" }],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn",
      workspaceMutation: "none",
    },
  },
  {
    name: "apply-patch-context-mismatch-failure",
    description: "Rejects an update whose context is absent and preserves the workspace.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Update File: README.txt\n@@\n-this line does not exist\n+replacement\n*** End Patch. After the tool rejects it, reply with exactly CONTEXT_MISMATCH_REJECTED.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["CONTEXT_MISMATCH_REJECTED"], requiredCompletedItemTypes: ["agent_message"],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "none",
    },
  },
  {
    name: "apply-patch-delete-missing-failure",
    description: "Rejects deletion of a missing file and preserves the workspace.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Delete File: definitely-missing.txt\n*** End Patch. After the tool rejects it, reply with exactly DELETE_MISSING_REJECTED.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["DELETE_MISSING_REJECTED"], requiredCompletedItemTypes: ["agent_message"],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "none",
    },
  },
  {
    name: "apply-patch-duplicate-add-overwrite",
    description: "Matches Rust by allowing Add File to overwrite an existing file.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt: "Call apply_patch exactly once with this exact patch and do not retry: *** Begin Patch\n*** Add File: README.txt\n+replacement\n*** End Patch. After the tool completes, reply with exactly DUPLICATE_ADD_APPLIED.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["DUPLICATE_ADD_APPLIED"], requiredCompletedItemTypes: ["file_change", "agent_message"],
      fileChanges: [{ status: "completed" }],
      eventSequenceComparison: "semantic-tools", agentMessageComparison: "final-per-turn", workspaceMutation: "required",
      workspaceChanges: [{ path: "README.txt", change: "modified" }],
    },
  },
  {
    name: "real-coding-move-delete",
    description: "Moves an implementation file, deletes an obsolete file, and verifies existing tests.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{
      prompt:
        "Inspect the fixture with exactly one shell command: Get-Content -LiteralPath .\\legacy.py, .\\obsolete.txt, .\\test_formatter.py. Then use exactly one apply_patch call that moves legacy.py to formatter.py without changing its code and deletes obsolete.txt. Do not modify test_formatter.py. Finally run exactly one shell command: python -m unittest -v test_formatter.py. After tests pass, reply with exactly CODING_MOVE_DELETE_OK.",
    }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["CODING_MOVE_DELETE_OK"],
      requiredCompletedItemTypes: ["file_change", "command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0 }, { status: "completed", exitCode: 0 }],
      eventSequenceComparison: "semantic-tools",
      agentMessageComparison: "final-per-turn",
      workspaceMutation: "required",
      workspaceRequiredPaths: ["formatter.py", "test_formatter.py"],
      compareWorkspacePaths: ["legacy.py", "formatter.py", "obsolete.txt", "test_formatter.py"],
    },
  },
  {
    name: "real-coding-modify",
    description: "Fixes an existing implementation through apply_patch and verifies existing tests.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "The existing Python tests describe the intended behavior, but calculator.py is incorrect. First run exactly one shell command to inspect both files: Get-Content -LiteralPath .\\calculator.py, .\\test_calculator.py. Then use apply_patch once to make the smallest possible fix to calculator.py only. Do not modify test_calculator.py. Finally run exactly one shell command: python -m unittest -v test_calculator.py. After tests pass, reply with exactly CODING_MODIFY_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["CODING_MODIFY_OK"],
      requiredCompletedItemTypes: ["file_change", "command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0 }, { status: "completed", exitCode: 0 }],
      workspaceMutation: "required",
      workspaceRequiredPaths: ["calculator.py", "test_calculator.py"],
      compareWorkspacePaths: ["calculator.py", "test_calculator.py"],
    },
  },
  {
    name: "real-coding-unittest",
    description: "Implements a small Python function, adds unit tests, and verifies behavior through the shell.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Work directly in the current fixture. Create calculator.py with a pure function add(left, right) that returns the numeric sum. Create test_calculator.py using only Python unittest with tests for 2+3=5, -2+5=3, and 0+0=0. Run exactly one shell command to execute the tests: python -m unittest -v test_calculator.py. Do not modify README.txt or any other file. After tests pass, reply with exactly CODING_TESTS_OK.",
      },
      {
        prompt:
          "Run exactly one shell command: python -c \"from calculator import add; assert add(2, 3) == 5 and add(-2, 5) == 3 and add(0, 0) == 0; print('CODE_BEHAVIOR_OK')\". Do not modify any file. Then reply with exactly CODING_VERIFY_OK.",
        resume: true,
      },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["CODING_TESTS_OK", "CODING_VERIFY_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [
        { status: "completed", exitCode: 0 },
        { status: "completed", exitCode: 0, output: "CODE_BEHAVIOR_OK" },
      ],
      commandOutputComparison: "status-exit-code",
      requireStableThreadId: true,
      workspaceMutation: "required",
      workspaceRequiredPaths: ["calculator.py", "test_calculator.py"],
      compareWorkspacePaths: ["calculator.py", "test_calculator.py"],
    },
  },
  {
    name: "command-mixed-output",
    description: "Preserves the Rust SDK aggregation contract for both stdout and stderr.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: cmd /d /c \"echo SDK_STDOUT&&echo SDK_STDERR 1>&2\". Do not send commentary before or after the tool call. Then reply with exactly MIXED_OUTPUT_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["MIXED_OUTPUT_OK"], requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "SDK_STDOUT\nSDK_STDERR" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "command-missing-file",
    description: "Reports deterministic command failure and stderr for a missing file without sandbox dependencies.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Get-Content -LiteralPath .\\definitely-missing-sdk-file.txt. Do not send commentary before or after the tool call. After it fails, reply with exactly MISSING_FILE_OBSERVED." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["MISSING_FILE_OBSERVED"], requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "failed", exitCode: 1, outputPattern: "Cannot find path|does not exist" }],
      commandOutputComparison: "status-exit-code",
      workspaceMutation: "none",
    },
  },
  {
    name: "command-space-path",
    description: "Reads a fixture whose relative path contains spaces.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Get-Content -LiteralPath '.\\folder with spaces\\value.txt' -Raw. Do not send commentary before or after the tool call. Then reply with exactly SPACE_PATH_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["SPACE_PATH_OK"], requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "SDK_SPACE_PATH_OK" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "approval-on-failure-read",
    description: "Uses on-failure approval for a successful read that must not request approval.",
    optIn: true,
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only", skipGitRepoCheck: true, approvalPolicy: "on-failure",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Get-Content -LiteralPath .\\README.txt -TotalCount 1. Do not request elevated permissions and do not send commentary before or after the tool call. Then reply with exactly ON_FAILURE_READ_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["ON_FAILURE_READ_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      forbiddenCompletedItemTypes: ["approval_request", "command_execution_approval", "file_change_approval"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "This fixture is intentionally tiny." }],
      workspaceMutation: "none",
    },
  },
  {
    name: "approval-untrusted-read",
    description: "Uses untrusted approval for an allowlisted read-only command.",
    optIn: true,
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only", skipGitRepoCheck: true, approvalPolicy: "untrusted",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Get-Content -LiteralPath .\\README.txt -TotalCount 1. Do not request elevated permissions and do not send commentary before or after the tool call. Then reply with exactly UNTRUSTED_READ_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["UNTRUSTED_READ_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      forbiddenCompletedItemTypes: ["approval_request", "command_execution_approval", "file_change_approval"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "This fixture is intentionally tiny." }],
      workspaceMutation: "none",
    },
  },
  {
    name: "command-multiline-output",
    description: "Preserves ordered multiline stdout from a deterministic command.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: 1..20 | ForEach-Object { 'SDK_LINE_' + $_ }. Do not send commentary before or after the tool call. Then reply with exactly MULTILINE_OUTPUT_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["MULTILINE_OUTPUT_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: Array.from({ length: 20 }, (_, index) => `SDK_LINE_${index + 1}`).join("\n") }],
      workspaceMutation: "none",
    },
  },
  {
    name: "command-empty-output",
    description: "Runs a successful command with no stdout or stderr.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Write-Output SDK_DISCARD_ME | Out-Null. Do not send commentary before or after the tool call. Then reply with exactly EMPTY_OUTPUT_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["EMPTY_OUTPUT_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "command-stderr-output",
    description: "Preserves deterministic stderr from a successful command.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: cmd /d /c \"echo SDK_STDERR_ONLY 1>&2\". Do not send commentary before or after the tool call. Then reply with exactly STDERR_OUTPUT_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["STDERR_OUTPUT_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "SDK_STDERR_ONLY" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "command-unicode-output",
    description: "Preserves UTF-8 command output across the SDK protocol.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [{ prompt: "Run exactly one shell command: Get-Content -LiteralPath .\\unicode.txt -Raw -Encoding UTF8. Do not send commentary before or after the tool call. Then reply with exactly UNICODE_OUTPUT_OK." }],
    expected: {
      terminal: "turn.completed", minAgentMessages: 1, requireUsage: true, expectedTurns: 1,
      exactAgentMessages: ["UNICODE_OUTPUT_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "SDK_中文_✓" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "tool-resume-read",
    description: "Resumes a persisted thread and executes a deterministic command in both turns.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access", skipGitRepoCheck: true, approvalPolicy: "never",
      networkAccessEnabled: false, webSearchMode: "disabled",
    },
    turns: [
      { prompt: "Run exactly one shell command: Get-Content -LiteralPath .\\README.txt -TotalCount 1. Do not send commentary before or after the tool call. Then reply with exactly TOOL_TURN1_OK." },
      { prompt: "Run exactly one shell command: (Get-Content -LiteralPath .\\README.txt | Where-Object { $_ -ne '' }).Count. Do not send commentary before or after the tool call. Then reply with exactly TOOL_TURN2_OK.", resume: true },
    ],
    expected: {
      terminal: "turn.completed", minAgentMessages: 2, requireUsage: true, expectedTurns: 2,
      exactAgentMessages: ["TOOL_TURN1_OK", "TOOL_TURN2_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [
        { status: "completed", exitCode: 0, output: "This fixture is intentionally tiny." },
        { status: "completed", exitCode: 0, output: "2" },
      ],
      requireStableThreadId: true,
      workspaceMutation: "none",
    },
  },
  {
    name: "approval-on-request-read",
    description: "Uses on-request approval for a read-only command that must not require approval.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "on-request",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Get-Content -LiteralPath .\\README.txt -TotalCount 1. Do not request elevated permissions and do not send commentary before or after the tool call. Then reply with exactly ON_REQUEST_READ_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["ON_REQUEST_READ_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      forbiddenCompletedItemTypes: ["approval_request", "command_execution_approval", "file_change_approval"],
      commandExecutions: [
        {
          status: "completed",
          exitCode: 0,
          output: "This fixture is intentionally tiny.",
        },
      ],
      workspaceMutation: "none",
    },
  },
  {
    name: "working-directory-mapping",
    description: "Verifies that the SDK workingDirectory option becomes the command process CWD.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Split-Path -Leaf (Get-Location). Do not send commentary before or after the tool call. Then reply with exactly WORKING_DIRECTORY_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["WORKING_DIRECTORY_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "workspace" }],
      workspaceMutation: "none",
    },
  },
  {
    name: "local-image-resume",
    description: "Persists a local-image turn and resumes it in a fresh CLI process.",
    timeoutMs: 120000,
    localImageFixture: "systemskills/assets/samples/openai-docs/assets/openai.png",
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Remember that this turn includes one local image. Reply with exactly IMAGE_TURN_OK.",
        includeLocalImage: true,
      },
      {
        prompt: "Return only the required JSON object confirming whether the previous turn included a local image.",
        resume: true,
        outputSchema: {
          type: "object",
          properties: { previousTurnHadImage: { type: "boolean", const: true } },
          required: ["previousTurnHadImage"],
          additionalProperties: false,
        },
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 2,
      requireUsage: true,
      expectedTurns: 2,
      agentMessageContracts: [{ exact: "IMAGE_TURN_OK" }, { structured: { previousTurnHadImage: true } }],
      requireStableThreadId: true,
      workspaceMutation: "none",
    },
  },
  {
    name: "local-image-input",
    description: "Sends the same local PNG through the SDK structured input API.",
    timeoutMs: 120000,
    localImageFixture: "systemskills/assets/samples/openai-docs/assets/openai.png",
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Confirm that one local image was attached. Return only the required JSON object.",
        includeLocalImage: true,
        outputSchema: {
          type: "object",
          properties: { imageAttached: { type: "boolean", const: true } },
          required: ["imageAttached"],
          additionalProperties: false,
        },
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      structuredAgentMessages: [{ imageAttached: true }],
      workspaceMutation: "none",
    },
  },
  {
    name: "danger-full-access-write",
    description: "Creates a deterministic file with danger-full-access and compares direct execution semantics.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "danger-full-access",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Set-Content -LiteralPath .\\full-access.txt -Value SDK_FULL_ACCESS_OK -NoNewline. Do not send commentary before or after the tool call and do not modify any other file. After it succeeds, reply with exactly FULL_ACCESS_WRITE_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["FULL_ACCESS_WRITE_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [{ status: "completed", exitCode: 0, output: "" }],
      workspaceMutation: "required",
      workspaceChanges: [
        {
          path: "full-access.txt",
          change: "added",
          hash: "5d45fa307bc29c800905f60abae6f684307b8c27421824b200be608aaaa032a0",
        },
      ],
    },
  },
  {
    name: "read-only-write-denied",
    description: "Attempts a deterministic write under read-only sandbox and verifies denial semantics.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Set-Content -LiteralPath .\\forbidden.txt -Value SDK_MUST_NOT_WRITE -NoNewline. Do not send commentary before or after the tool call. After it fails, reply with exactly READ_ONLY_DENIED.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["READ_ONLY_DENIED"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      forbiddenCompletedItemTypes: ["approval_request", "command_execution_approval", "file_change_approval"],
      commandExecutions: [
        {
          status: "failed",
          exitCode: 1,
          outputPattern: "GetContentWriterUnauthorizedAccessError",
        },
      ],
      commandOutputComparison: "status-exit-code",
      workspaceMutation: "none",
    },
  },
  {
    name: "additional-directory-read",
    description: "Reads a deterministic file from an SDK additional directory outside the workspace.",
    timeoutMs: 120000,
    additionalDirectoryMode: "fixture",
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Get-Content -LiteralPath ..\\additional\\outside.txt. Do not send commentary before or after the tool call. Then reply with exactly ADDITIONAL_DIRECTORY_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["ADDITIONAL_DIRECTORY_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [
        {
          status: "completed",
          exitCode: 0,
          output: "SDK_ADDITIONAL_DIRECTORY_FIXTURE",
        },
      ],
      workspaceMutation: "none",
    },
  },
  {
    name: "abort-before-run",
    description: "Uses an already-aborted signal and verifies that the SDK terminates before a model turn.",
    timeoutMs: 30000,
    abortBeforeRun: true,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "This request must be aborted before execution.",
      },
    ],
    expected: {
      outcome: "failure",
      errorPattern: "abort|aborted|operation was aborted|signal",
      terminal: "turn.completed",
      minAgentMessages: 0,
      requireUsage: false,
      expectedTurns: 0,
      workspaceMutation: "none",
    },
  },
  {
    name: "invalid-working-directory",
    description: "Validates SDK and CLI failure semantics for a missing working directory.",
    timeoutMs: 30000,
    workingDirectoryMode: "missing",
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "This request must not reach the model.",
      },
    ],
    expected: {
      outcome: "failure",
      errorPattern: "working directory.*invalid|os error 2|cannot find.*(path|directory)",
      terminal: "turn.completed",
      minAgentMessages: 0,
      requireUsage: false,
      expectedTurns: 0,
      workspaceMutation: "none",
    },
  },
  {
    name: "streaming-smoke",
    description: "Single turn, text-only live model call; validates SDK event lifecycle.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Reply with exactly this text and nothing else: SDK_PARITY_SMOKE_OK",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["SDK_PARITY_SMOKE_OK"],
      workspaceMutation: "none",
    },
  },
  {
    name: "workspace-structured-read",
    description: "Reads a fixed workspace fixture through the shell and returns schema-constrained facts.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Get-Content -LiteralPath .\\README.txt. Do not send commentary before or after the tool call. Then return its first line and number of non-empty lines.",
        outputSchema: {
          type: "object",
          properties: {
            firstLine: { type: "string" },
            nonEmptyLineCount: { type: "integer" },
          },
          required: ["firstLine", "nonEmptyLineCount"],
          additionalProperties: false,
        },
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      structuredAgentMessages: [
        {
          firstLine: "This fixture is intentionally tiny.",
          nonEmptyLineCount: 2,
        },
      ],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      workspaceMutation: "none",
    },
  },
  {
    name: "workspace-file-write",
    description: "Creates a deterministic file and compares the resulting workspace tree.",
    timeoutMs: 240000,
    threadOptions: {
      sandboxMode: "workspace-write",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: Set-Content -LiteralPath .\\result.txt -Value SDK_FILE_WRITE_OK -NoNewline. Do not send commentary before or after the tool call and do not modify any other file. After the command succeeds, reply with exactly FILE_WRITE_OK.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["FILE_WRITE_OK"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      workspaceMutation: "required",
      workspaceChanges: [
        {
          path: "result.txt",
          change: "added",
          hash: "22f219456a7255a49dd673a20a667062a91ef1d6c721650f47584101474387d2",
        },
      ],
    },
  },
  {
    name: "command-nonzero-exit",
    description: "Runs a deterministic failing shell command and validates its SDK item semantics.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt:
          "Run exactly one shell command: powershell -NoProfile -Command \"Write-Output SDK_EXIT_7; exit 7\". Do not send commentary before or after the tool call. After it finishes, reply with exactly COMMAND_EXIT_7_OBSERVED.",
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 1,
      requireUsage: true,
      expectedTurns: 1,
      exactAgentMessages: ["COMMAND_EXIT_7_OBSERVED"],
      requiredCompletedItemTypes: ["command_execution", "agent_message"],
      commandExecutions: [
        {
          status: "failed",
          exitCode: 1,
          output: "SDK_EXIT_7",
        },
      ],
      workspaceMutation: "none",
    },
  },
  {
    name: "persistent-resume",
    description: "Runs two turns and resumes the persisted thread by ID in a fresh CLI process.",
    timeoutMs: 120000,
    threadOptions: {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
    },
    turns: [
      {
        prompt: "Remember the token RESUME_TOKEN_7F3A and reply with exactly TURN1_OK.",
      },
      {
        prompt: "Reply with the token I asked you to remember, and nothing else.",
        resume: true,
      },
    ],
    expected: {
      terminal: "turn.completed",
      minAgentMessages: 2,
      requireUsage: true,
      expectedTurns: 2,
      exactAgentMessages: ["TURN1_OK", "RESUME_TOKEN_7F3A"],
      requireStableThreadId: true,
      workspaceMutation: "none",
    },
  },
];

export function getScenario(name: string): Scenario {
  const scenario = scenarios.find((item) => item.name === name);
  if (!scenario) {
    throw new Error(`Unknown scenario: ${name}`);
  }
  return scenario;
}
