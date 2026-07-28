import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { collectRolloutRecords, selectRolloutJsonl, snapshotFiles } from "./util.ts";

test("workspace snapshots ignore Python bytecode caches", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-snapshot-"));
  try {
    mkdirSync(path.join(root, "__pycache__"));
    mkdirSync(path.join(root, "nested"));
    writeFileSync(path.join(root, "keep.py"), "VALUE = 1\n");
    writeFileSync(path.join(root, "__pycache__", "keep.cpython-312.pyc"), "cache");
    writeFileSync(path.join(root, "nested", "ignored.pyc"), "cache");
    writeFileSync(path.join(root, "nested", "ignored.pyo"), "cache");

    assert.deepEqual(Object.keys(snapshotFiles(root)), ["keep.py"]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("rollout selection prefers the complete matching thread record", () => {
  const threadId = "thread-screen-capture";
  const initial = `${threadId}\nuser`;
  const complete = `${threadId}\nuser\nassistant\nimageView`;
  assert.equal(selectRolloutJsonl([initial, "another-thread\nlonger-unrelated-record", complete], threadId), complete);
});

test("rollout selection matches session metadata instead of a child parent reference", () => {
  const rootID = "thread-root";
  const root = `${JSON.stringify({ type: "session_meta", payload: { id: rootID, thread_source: "cli" } })}\nroot`;
  const child = `${JSON.stringify({
    type: "session_meta",
    payload: {
      id: "thread-child",
      session_id: rootID,
      thread_source: "subagent",
      source: { subagent: { thread_spawn: { parent_thread_id: rootID } } },
      agent_path: "/root/calculator",
    },
  })}\n${"child\n".repeat(20)}`;

  assert.equal(selectRolloutJsonl([child, root], rootID), root);
  assert.deepEqual(collectRolloutRecords([root, child]).map(({ jsonl: _jsonl, ...record }) => record), [
    { threadId: rootID, sessionId: "", threadSource: "cli", parentThreadId: "", agentPath: "", agentNickname: "" },
    { threadId: "thread-child", sessionId: rootID, threadSource: "subagent", parentThreadId: rootID, agentPath: "/root/calculator", agentNickname: "" },
  ]);
});
