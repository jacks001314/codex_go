package tool

// Code Mode tool descriptions are byte-identical to the Rust
// codex-rs/code-mode-protocol/src/description.rs templates. Catalog overrides
// replace them wholesale, but the bundled text is what an unconfigured model
// sees, so paraphrasing it would silently change model guidance.

// CodeModeExecDescriptionTemplate is the Rust EXEC_DESCRIPTION_TEMPLATE. It
// keeps the {{ default_exec_yield_time_ms }} and {{ image_helper }} placeholders.
const CodeModeExecDescriptionTemplate = "Run JavaScript code to orchestrate/compose tool calls\n" +
	"- Evaluates the provided JavaScript code in a fresh V8 isolate as an async module.\n" +
	"- All nested tools are available on the global `tools` object, for example `await tools.exec_command(...)`. Tool names are exposed as normalized JavaScript identifiers, for example `await tools.mcp__ologs__get_profile(...)`.\n" +
	"- Nested tool methods take either a string or an object as their input argument.\n" +
	"- Nested tools return either an object or a string, based on the description.\n" +
	"- Runs raw JavaScript -- no Node, no file system, no network access, no console.\n" +
	"- Accepts raw JavaScript source text, not JSON, quoted strings, or markdown code fences.\n" +
	"- You may optionally start the tool input with a first-line pragma like `// @exec: {\"yield_time_ms\": 10000, \"max_output_tokens\": 1000}`.\n" +
	"- `yield_time_ms` asks `exec` to yield early if the script is still running. Defaults to {{ default_exec_yield_time_ms }} ms.\n" +
	"- `max_output_tokens` sets the token budget for direct `exec` results. Defaults to 10000 tokens.\n" +
	"- When the JS code is fully evaluated, the isolate's lifetime ends and unawaited promises are silently discarded.\n" +
	"\n" +
	"- Global helpers:\n" +
	"- `exit()`: Immediately ends the current script successfully (like an early return from the top level).\n" +
	"- `text(value: string | number | boolean | undefined | null)`: Appends a text item. Non-string values are stringified with `JSON.stringify(...)` when possible.\n" +
	"- {{ image_helper }}\n" +
	"- `audio(audioUrlOrItem: string | { audio_url: string } | AudioContent)`: Appends an audio item. `audio_url` should be a base64-encoded `data:` URL. To forward an MCP tool audio block, pass an individual `AudioContent` block from `result.content`, for example `audio(result.content[0])`.\n" +
	"- `generatedImage(result: { image_url: string; output_hint?: string })`: Appends an image-generation result and its optional output hint. HTTP(S) URLs are not supported.\n" +
	"- `store(key: string, value: any)`: stores a serializable value under a string key for later `exec` calls in the same session.\n" +
	"- `load(key: string)`: returns the stored value for a string key, or `undefined` if it is missing.\n" +
	"- `notify(value: string | number | boolean | undefined | null)`: immediately injects an extra `custom_tool_call_output` for the current `exec` call. Values are stringified like `text(...)`.\n" +
	"- `setTimeout(callback: () => void, delayMs?: number)`: schedules a callback to run later and returns a timeout id. Pending timeouts do not keep `exec` alive by themselves; await an explicit promise if you need to wait for one.\n" +
	"- `clearTimeout(timeoutId?: number)`: cancels a timeout created by `setTimeout`.\n" +
	"- `ALL_TOOLS`: metadata for the enabled nested tools as `{ name, description }` entries.\n" +
	"- `yield_control()`: yields the accumulated output to the model immediately while the script keeps running."

// CodeModeLegacyImageHelperDescription is the Rust LEGACY_IMAGE_HELPER_DESCRIPTION,
// used when image detail values are visible to the model.
const CodeModeLegacyImageHelperDescription = "`image(imageUrlOrItem: string | { image_url: string; detail?: \"auto\" | \"low\" | \"high\" | \"original\" | null } | ImageContent, detail?: \"auto\" | \"low\" | \"high\" | \"original\" | null)`: Appends an image item. `image_url` should be a base64-encoded `data:` URL. To forward an MCP tool image, pass an individual `ImageContent` block from `result.content`, for example `image(result.content[0])`. MCP image blocks may request detail with `_meta: { \"codex/imageDetail\": \"original\" }`. When provided, the second `detail` argument overrides any detail embedded in the first argument."

// CodeModeUnifiedImageHelperDescription is the Rust UNIFIED_IMAGE_HELPER_DESCRIPTION,
// used when the unified image budget hides image detail values.
const CodeModeUnifiedImageHelperDescription = "`image(imageUrlOrItem: string | { image_url: string } | ImageContent)`: Appends an image item. `image_url` should be a base64-encoded `data:` URL. To forward an MCP tool image, pass an individual `ImageContent` block from `result.content`, for example `image(result.content[0])`."

// CodeModeWaitDescriptionTemplate is the Rust WAIT_DESCRIPTION_TEMPLATE, appended
// after the bundled wait headline.
const CodeModeWaitDescriptionTemplate = "- Use `wait` only after `exec` returns `Script running with cell ID ...`.\n" +
	"- `cell_id` identifies the running `exec` cell to resume.\n" +
	"- `yield_time_ms` controls how long to wait for more output before yielding again. Defaults to 10000 ms.\n" +
	"- `max_tokens` limits how much new output this wait call returns. Defaults to 10000 tokens.\n" +
	"- `terminate: true` stops the running cell; false or omitted waits for output.\n" +
	"- `wait` returns only the new output since the last yield, or the final completion or termination result for that cell.\n" +
	"- If the cell is still running, `wait` may yield again with the same `cell_id`.\n" +
	"- If the cell has already finished, `wait` returns the completed result and closes the cell."

// CodeModeToolSearchGuidance is the Rust TOOL_SEARCH_GUIDANCE, appended to the
// deferred-tool guidance when a ranked tool_search tool is present outside
// code_mode_only.
const CodeModeToolSearchGuidance = "Use `await tools.tool_search({query: \"...\", limit: 8})` for BM25-ranked discovery of deferred tools (up to `limit` matches; 8 by default). It returns `{tools: [{name, description}]}` with callable names and full TypeScript declarations. Print results with `text()` and inspect unfamiliar declarations before calling `tools[result.name](arguments)` in a later execution."
