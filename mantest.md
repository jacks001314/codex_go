# Manual TUI Test — sync29 New TUI Features

This is a manual test checklist for the Codex Go TUI features added in the sync29
alignment. It covers two features:

1. **Vim composer search + operator-search** (`#41586` / `tui/tea/vim_mode.go`).
2. **Model picker async catalog refresh + service-tier command refresh** (`#41467` / `tui/tea/model_picker.go`, `app/interactive.go`).

Each test lists **steps** and the **expected** result. Record PASS/FAIL in a
totals row at the bottom.

---

## Prerequisites

- **Build the binary** (Windows):

  ```powershell
  .\scripts\build.ps1 -Output sdktests\.tmp\bin\codex-go.exe -CGO off
  ```

  (Linux/macOS: `./scripts/build.sh`.)

- **Launch the interactive TUI** from the repo root:

  ```powershell
  .\sdktests\.tmp\bin\codex-go.exe
  ```

  (The interactive TUI is the default; `codex-go interactive` also works.)

- **Auth**: a valid API key or ChatGPT login is required for the model-picker
  account-catalog refresh (feature 2). The Vim search (feature 1) needs no auth.
- **`fast_mode` feature** (for 2.4): enable it so service-tier slash commands are
  shown. Set `[features] fast_mode = true` in the project `config.toml` (or run
  with `--enable fast_mode`).
- **Text setup**: for the Vim tests, type a two-word sentence such as
  `hello world hello` in the composer.

---

## Feature 1 — Vim composer search + operator-search

### 1.1 Enable Vim mode

**Steps**

1. In the composer, type `/vim` and press Enter.
2. Observe the notice and the composer mode.

**Expected**

- Notice `Vim mode enabled.`.
- The composer enters **normal mode** (the insert caret/prompt disappears; keys
  are now Vim motions rather than text).
- `/vim` again toggles back to insert-typing mode.

---

### 1.2 Composer text search (`/` and `?`)

**Steps**

1. With Vim mode on, press `i`, type `hello world hello`, press Esc.
2. Press `/` (forward search).
3. Type `hello`. Observe the live footer.
4. Press Enter.
5. Press `n`, then `n`, then `N`, then `N`.
6. Press `?` (backward search), type `hello`, press Enter.

**Expected**

- While typing after `/`, the footer shows `/hello`; after `?`, it shows
  `?hello` (a search-query footer with the direction sigil + typed query).
- After `/hello` + Enter, the cursor lands on the first `hello` (byte offset 0).
- `n` moves to the second `hello` (byte offset 12); a further `n` wraps back to
  the first match; `N` moves to the previous match (wrapping).
- After `?hello` + Enter, the cursor lands on the last `hello` before the cursor
  (byte offset 12 from the start position).
- Esc while a search query is active cancels it and clears the footer.

Result: ______ (PASS/FAIL)

---

### 1.3 Operator-search (`d/`, `y/`, `c/`, `d?`, `dn`)

**Setup:** `hello world hello`, Vim normal mode, move the cursor to byte offset 6
(just before `world`). Press `l` six times (or `0` then `l` x 6) to position the
cursor there.

**3.1 `d/` — delete to the next match**

1. Press `d`, then `/`, type `hello`, press Enter.

**Expected:** the text from the cursor (offset 6) to the next `hello` (offset 12)
is deleted, leaving `hello hello`.

**3.2 `y/` — yank to the next match**

1. Recreate `hello world hello`, cursor at offset 6.
2. Press `y`, then `/`, type `hello`, press Enter.

**Expected:** the yank register holds `world ` (offset 6..12); the composer text
is unchanged; the cursor moves to the start of the range.

**3.3 `c/` — change to the next match**

1. Recreate `hello world hello`, cursor at offset 6.
2. Press `c`, then `/`, type `hello`, press Enter.

**Expected:** the range is deleted, leaving `hello hello`, and the TUI enters
insert mode at the start of the edit.

**3.4 `d?` — delete back to the previous match**

1. Recreate `hello world hello`, cursor at offset 6.
2. Press `d`, then `?`, type `hello`, press Enter.

**Expected:** the text back to the previous `hello` (offset 0) is deleted,
leaving `world hello`.

**3.5 `dn` — operator on the next match of the last query**

1. Recreate `hello world hello`, cursor at offset 0.
2. Press `/`, type `hello`, Enter (lands at the first match, offset 0).
3. Press `d`, then `n`.

**Expected:** `n` moves the search to the next match (offset 12) and the pending
`d` operator deletes the cursor→next-match range, leaving `hello` (offset 0..12
removed). This is a delete, not a plain cursor move.

**Note:** an operator-search where the cursor is already on the match start is a
no-op (Rust `apply_vim_search` returns early when `origin == target`).

Result: ______ (PASS/FAIL)

---

## Feature 2 — Model picker async catalog refresh + service-tier refresh

### 2.1 Open the model picker and cached list

**Steps**

1. Type `/model` and press Enter.

**Expected**

- A model picker modal opens (title `Select Model`).
- The cached model list is shown immediately (configured/bundled catalog).
- The current model is highlighted; the default model is marked `default`.

---

### 2.2 Async catalog refresh (in-place)

**Steps**

1. With the model picker open (from 2.1), wait a moment while the picker fetches
   the current model catalog from the configured provider (via
   `interactiveOnListModels`).
2. Observe the picker without closing it.

**Expected (authenticated ChatGPT / API-key account)**

- The open picker refreshes in place to the account's current model catalog
  (the highlighted model is preserved when it is still present).
- If the current model is no longer in the refreshed catalog, the picker falls
  back to the catalog default (or the first available model).
- Stale / failed / empty refresh responses are ignored (the picker keeps the
  cached choice; no error is surfaced).
- **Observability caveat:** this in-place refresh is only visible when the
  fetched catalog actually differs from the cached one. If the account catalog
  is unchanged, the picker stays exactly as-is (an `unchanged` refresh is a
  no-op by design). To observe a visible refresh, the cached catalog must be
  stale relative to the account (e.g. a model was added/removed or the default
  changed server-side).

**Expected (unauthenticated or offline run)**

- The picker keeps the static/configured catalog; no visible error. This is the
  graceful fallback (the refresh callback returns nil and the tea Model ignores
  it).

Result: ______ (PASS/FAIL)

---

### 2.3 Reasoning submenu refresh in place

**Steps**

1. Open `/model`, select a model that has reasoning levels — this opens the
   reasoning picker (`Select Reasoning Level ...`).
2. While the reasoning modal is open, let a catalog refresh arrive for that
   model whose reasoning levels have changed.

**Expected**

- The open reasoning submenu refreshes in place with the updated option list.
- The currently-selected reasoning is preserved when it is still supported.
- **Observability caveat:** as in 2.2, this is only visible when the refreshed
  catalog changes that model's reasoning levels; otherwise the submenu stays
  unchanged.

Result: ______ (PASS/FAIL)

---

### 2.4 Service-tier command refresh on model change

**Prerequisite:** enable the `fast_mode` feature so service-tier slash commands
are shown (see Prerequisites).

**Steps**

1. With the current model (e.g. one that exposes a fast/priority service tier),
   type `/` to open the slash popup.
2. Confirm a service-tier entry (e.g. `/fast`) appears immediately after
   `/model`.
3. Open `/model`, pick a different model (one with different service tiers).
4. Type `/` again and inspect the service-tier entries.

**Expected**

- After step 2, the service-tier slash entry (e.g. `/fast`) reflects the
  **current** model's service tiers.
- After stepping to a model with different tiers, the service-tier slash entries
  update to the **new** model's tiers (the entry appears/disappears or changes
  accordingly) — they must not remain pinned to the old model.

**Note:** the account-scoped catalog refresh (2.2) plus this refresh keeps the
service-tier surface aligned with the effective account catalog / active model.

Result: ______ (PASS/FAIL)

---

## Totals

| Test | Result |
|------|--------|
| 1.1 Enable Vim mode | |
| 1.2 Composer text search | |
| 1.3 Operator-search | |
| 2.1 Open model picker | |
| 2.2 Async catalog refresh | |
| 2.3 Reasoning submenu refresh | |
| 2.4 Service-tier command refresh | |

Notes / failures: __________________________________________________
