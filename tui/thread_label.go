package tui

// Rust parity: codex-rs/tui/src/thread_label.rs (#61742). The cloud marker is a
// display prefix only: thread names, search text, and identities stay unchanged.

// CloudThreadLabelPrefix marks a conversation owned by the hosted Cloud
// backend. U+FE0F requests emoji presentation, matching Rust's cloud
// prefix (#62573).
const CloudThreadLabelPrefix = "\u2601\uFE0F "

// ThreadLabelPrefix returns the display prefix for a thread label.
func ThreadLabelPrefix(cloud bool) string {
	if cloud {
		return CloudThreadLabelPrefix
	}
	return ""
}

// ThreadLabelPrefixWidth is the display width the marker takes from a label's
// truncation budget.
func ThreadLabelPrefixWidth(cloud bool) int {
	return DisplayWidth(ThreadLabelPrefix(cloud))
}

// DecorateThreadLabel prefixes a thread label without touching its identity.
func DecorateThreadLabel(label string, cloud bool) string {
	return ThreadLabelPrefix(cloud) + label
}
