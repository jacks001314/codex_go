package context

import "testing"

// TestParseDelegatedToolOutputMatchesRustTrustAndEscaping mirrors Rust #41046:
// only create_thread / send_message_to_thread outputs from the codex_tui and
// codex_app namespaces are delegation prompts, and `&lt;`/`&gt;` decode before
// `&amp;` so doubly escaped text stays escaped.
func TestParseDelegatedToolOutputMatchesRustTrustAndEscaping(t *testing.T) {
	const prompt = "<codex_delegation>\n  <source_thread_id>src</source_thread_id>\n  <input>&amp;lt;main&amp;gt; &amp; done</input>\n</codex_delegation>"
	for _, namespace := range []string{"codex_tui", "codex_app"} {
		source, delegated, ok := ParseDelegatedToolOutput("create_thread", namespace, prompt)
		if !ok || source != "src" || delegated != "&lt;main&gt; & done" {
			t.Fatalf("%s delegated = (%q,%q,%v)", namespace, source, delegated, ok)
		}
	}
	if _, _, ok := ParseDelegatedToolOutput("create_thread", "collaboration", prompt); ok {
		t.Fatalf("collaboration namespace was accepted")
	}
	if _, _, ok := ParseDelegatedToolOutput("create_thread", "untrusted", prompt); ok {
		t.Fatalf("untrusted namespace was accepted")
	}
	if _, _, ok := ParseDelegatedToolOutput("other", "codex_tui", prompt); ok {
		t.Fatalf("non-delegation tool was accepted")
	}
}
