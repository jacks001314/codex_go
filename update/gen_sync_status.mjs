#!/usr/bin/env node
// Regenerates a reproducible snapshot of the daily Rust->Go sync round.
// Usage: node update/gen_sync_status.mjs <YYYY_MM_DD>
// Reads parity/commits.json (the audited ledger) and the local git history, so
// the counts in the snapshot can be re-derived from repository state.
import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

const date = process.argv[2];
if (!date || !/^\d{4}_\d{2}_\d{2}$/.test(date)) {
  console.error("usage: node update/gen_sync_status.mjs <YYYY_MM_DD>");
  process.exit(2);
}

const repo = new URL("..", import.meta.url);
const git = (...args) => execFileSync("git", args, { cwd: repo, encoding: "utf8" }).trim();

const ledger = JSON.parse(readFileSync(new URL("../parity/commits.json", import.meta.url), "utf8"));
const counts = {};
const partial = [];
const blocked = [];
const pending = [];
for (const entry of ledger.commits) {
  counts[entry.status] = (counts[entry.status] ?? 0) + 1;
  const pr = (entry.subject.match(/\(#(\d+)\)/) ?? [])[1];
  if (entry.status === "partial") partial.push(pr ? "#" + pr : entry.sha.slice(0, 10));
  if (entry.status === "blocked_precondition") blocked.push(pr ? "#" + pr : entry.sha.slice(0, 10));
  if (entry.status === "implementation_pending") pending.push(pr ? "#" + pr : entry.sha.slice(0, 10));
}

// Go commits authored on the snapshot day carry this round's work.
const day = date.replaceAll("_", "-");
const round = git("log", "--format=%H|%ad|%s", "--date=short", "-80")
  .split("\n")
  .filter((line) => line.split("|")[1] === day)
  .map((line) => {
    const [sha, , subject] = line.split("|");
    return { sha: sha.slice(0, 8), subject };
  });

const snapshot = {
  date,
  source: "Rust openai/codex -> Go jacks001314/codex_go",
  rustRange: { fromExclusive: ledger.rustFromExclusive, toInclusive: ledger.rustToInclusive },
  goHead: git("rev-parse", "HEAD"),
  ledger: { total: ledger.commits.length, counts, implementationPending: pending, partial, blockedPrecondition: blocked },
  roundCommits: round,
};
const out = new URL("../update/sync_status_" + date + ".json", import.meta.url);
writeFileSync(out, JSON.stringify(snapshot, null, 2) + "\n");
console.log("wrote", "update/sync_status_" + date + ".json");
