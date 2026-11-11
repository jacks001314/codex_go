package codemode

import (
	"github.com/grafana/sobek"

	"codex_go/utils"
)

const (
	contentHelperExpectsMessage = "content expects one MCP ContentBlock"
	contentEncryptedMessage     = "content cannot emit encrypted MCP text"
)

// normalizeSobekContent mirrors Rust normalize_output_content: a single MCP
// ContentBlock becomes its typed output item, while encrypted text is rejected
// (Rust #60665). Iterables are expanded by the JavaScript helper before each
// block reaches this callback.
func normalizeSobekContent(value sobek.Value) (ContentItem, string) {
	if sobek.IsUndefined(value) || sobek.IsNull(value) {
		return ContentItem{}, contentHelperExpectsMessage
	}
	exported := value.Export()
	if _, ok := exported.(map[string]any); !ok {
		return ContentItem{}, contentHelperExpectsMessage
	}
	item, err := utils.FromMCPContentBlock(exported)
	if err != nil {
		return ContentItem{}, contentHelperExpectsMessage
	}
	switch item.Kind {
	case utils.ContentInputText:
		return InputText(item.Text), ""
	case utils.ContentInputImage:
		detail := ImageDetail(utils.DefaultImageDetail)
		if item.Detail != nil {
			detail = ImageDetail(*item.Detail)
		}
		return InputImage(item.ImageURL, &detail), ""
	case utils.ContentInputAudio:
		return audioOutputItem(item.AudioURL), ""
	case utils.ContentEncryptedContent:
		return ContentItem{}, contentEncryptedMessage
	default:
		return ContentItem{}, contentHelperExpectsMessage
	}
}

// sobekContentHelpers ports codex-rs/code-mode-runtime/src/runtime/content.js
// (#60665). It expands iterables in order and closes the iterator when a block
// is rejected: the host callback throws, so the for-of loop runs return().
const sobekContentHelpers = `
(() => {
  const emitBlock = globalThis.content;
  const iteratorKey = Symbol.iterator;
  const apply = Reflect.apply;

  globalThis.content = function content(value) {
    const iterate =
      typeof value === "string" ? undefined : value?.[iteratorKey];
    if (typeof iterate !== "function") {
      return emitBlock(value);
    }

    const blocks = { [iteratorKey]: () => apply(iterate, value, []) };
    for (const block of blocks) {
      emitBlock(block);
    }
  };
})();
`
