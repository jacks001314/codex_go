#!/usr/bin/env node
// Chrome A/B harness for the inline visualization viewer CSP (Rust #58436).
//
// The viewer is an `iframe sandbox="allow-scripts" srcdoc="...">` shell, and a
// srcdoc frame inherits the shell document's Content-Security-Policy. The bug
// this guards against cannot be seen from a DOM shim or a string assertion: the
// shell policy
//   `default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'`
// silently blocks the injected tab/carousel runtime inside the frame, so the
// document looks correct on disk while the controls never exist at runtime.
//
// This script loads a viewer document in headless Chrome, attaches to the
// sandboxed frame (a sandboxed srcdoc frame has an opaque origin, so Chrome
// runs it out of process), and reports whether the runtime actually produced
// its live controls:
//   live_controls - `.viz-carousel-controls` exists in the frame
//   partial       - the runtime started (panels got role="group") but no controls
//   blocked       - the runtime never ran (the pre-fix shell CSP)
//
// Usage:
//   node scripts/inline_visualization_csp_ab.mjs --chrome <path> \
//     --before before.html --after after.html [--timeout-ms 8000]
//
// Exit codes: 0 = verdict printed, 2 = Chrome not available (callers skip),
// 3 = the probe itself failed.
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawn } from "node:child_process";
import { pathToFileURL } from "node:url";
import path from "node:path";
import process from "node:process";

const PROBE = `(() => {
  const controls = document.querySelector(".viz-carousel-controls");
  if (controls) { return "live_controls"; }
  const marked = document.querySelector('[data-variant][role="group"]');
  return marked ? "partial" : "blocked";
})()`;

function parseArgs(argv) {
  const options = { timeoutMs: 8000, chrome: process.env.CODEX_CHROME_PATH || "" };
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (flag === "--chrome") { options.chrome = value; index += 1; }
    else if (flag === "--before") { options.before = value; index += 1; }
    else if (flag === "--after") { options.after = value; index += 1; }
    else if (flag === "--timeout-ms") { options.timeoutMs = Number(value); index += 1; }
  }
  return options;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function cleanupProfileDir(profileDir) {
  try {
    rmSync(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  } catch {
    // Windows keeps the profile directory locked until Chrome exits.
  }
}

async function launchChrome(chrome) {
  const profileDir = mkdtempSync(path.join(tmpdir(), "codex-csp-ab-"));
  const child = spawn(
    chrome,
    [
      "--headless=new",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-extensions",
      "--remote-debugging-port=0",
      `--user-data-dir=${profileDir}`,
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  const portFile = path.join(profileDir, "DevToolsActivePort");
  const deadline = Date.now() + 15000;
  let port = 0;
  while (!port) {
    if (Date.now() > deadline) {
      try {
        child.kill();
      } catch {}
      cleanupProfileDir(profileDir);
      throw new Error("Chrome did not open a DevTools port");
    }
    if (existsSync(portFile)) {
      // Chrome creates the file before it finishes writing the port.
      try {
        const text = readFileSync(portFile, "utf8").split("\n")[0].trim();
        if (/^\d+$/.test(text)) {
          port = Number(text);
        }
      } catch {}
    }
    if (!port) {
      await sleep(50);
    }
  }
  return {
    port,
    close() {
      try {
        child.kill();
      } catch {}
      cleanupProfileDir(profileDir);
    },
  };
}

function openSocket(url) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    socket.onopen = () => resolve(socket);
    socket.onerror = () => reject(new Error("cannot connect to the DevTools socket"));
  });
}

async function probeDocument(port, htmlPath, timeoutMs) {
  const pageURL = pathToFileURL(path.resolve(htmlPath)).href;
  const response = await fetch(
    `http://127.0.0.1:${port}/json/new?${encodeURIComponent(pageURL)}`,
    { method: "PUT" },
  );
  if (!response.ok) {
    throw new Error(`cannot open the viewer document: HTTP ${response.status}`);
  }
  const target = await response.json();
  const socket = await openSocket(target.webSocketDebuggerUrl);
  let nextID = 0;
  const pending = new Map();
  // contextKey -> { sessionId, contextId }; only child targets are recorded.
  const frameContexts = new Map();
  let send;

  socket.onmessage = (event) => {
    try {
      const message = JSON.parse(event.data);
      if (message.id && pending.has(message.id)) {
        const { resolve, reject } = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) { reject(new Error(JSON.stringify(message.error))); }
        else { resolve(message.result); }
        return;
      }
      const sessionId = message.sessionId || "";
      if (message.method === "Target.attachedToTarget") {
        const info = message.params.targetInfo;
        const childSession = message.params.sessionId;
        if (info.type === "iframe" && childSession) {
          // The sandboxed srcdoc frame runs as its own target; enable Runtime
          // there so its execution contexts are reported to us.
          send("Runtime.enable", {}, childSession).catch(() => {});
        }
        return;
      }
      if (message.method === "Runtime.executionContextCreated") {
        const context = message.params.context;
        const frameId = context.auxData ? context.auxData.frameId : "";
        if (!sessionId || !frameId) {
          return;
        }
        frameContexts.set(`${sessionId}|${context.id}`, { sessionId, contextId: context.id });
      } else if (message.method === "Runtime.executionContextDestroyed") {
        frameContexts.delete(`${sessionId}|${message.params.executionContextId}`);
      } else if (message.method === "Runtime.executionContextsCleared") {
        for (const key of [...frameContexts.keys()]) {
          if (key.startsWith(`${sessionId}|`)) {
            frameContexts.delete(key);
          }
        }
      }
    } catch {
      // A malformed or late event must never abort the probe.
    }
  };

  send = (method, params = {}, sessionId = "") =>
    new Promise((resolve, reject) => {
      const id = (nextID += 1);
      pending.set(id, { resolve, reject });
      const payload = { id, method, params };
      if (sessionId) {
        payload.sessionId = sessionId;
      }
      socket.send(JSON.stringify(payload));
    });

  try {
    await send("Page.enable");
    await send("Runtime.enable");
    await send("Target.setAutoAttach", {
      autoAttach: true,
      waitForDebuggerOnStart: false,
      flatten: true,
    });
    const deadline = Date.now() + timeoutMs;
    await sleep(150);
    while (Date.now() < deadline) {
      for (const [key, entry] of [...frameContexts.entries()]) {
        let value;
        try {
          const result = await send(
            "Runtime.evaluate",
            { expression: PROBE, contextId: entry.contextId, returnByValue: true },
            entry.sessionId,
          );
          value = result && result.result ? result.result.value : undefined;
        } catch {
          // The frame navigated or was replaced; wait for its new context.
          frameContexts.delete(key);
          continue;
        }
        if (value === "live_controls" || value === "partial" || value === "blocked") {
          return value;
        }
      }
      await sleep(100);
    }
    return "blocked";
  } finally {
    socket.close();
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (!options.chrome || !existsSync(options.chrome)) {
    console.error("Chrome is not available; set --chrome or CODEX_CHROME_PATH");
    process.exit(2);
  }
  for (const key of ["before", "after"]) {
    if (!options[key] || !existsSync(options[key])) {
      console.error(`missing --${key} document`);
      process.exit(3);
    }
  }
  const chrome = await launchChrome(options.chrome);
  try {
    const verdict = {
      before: await probeDocument(chrome.port, options.before, options.timeoutMs),
      after: await probeDocument(chrome.port, options.after, options.timeoutMs),
    };
    process.stdout.write(`${JSON.stringify(verdict)}\n`);
  } finally {
    chrome.close();
  }
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : String(error));
  process.exit(3);
});
