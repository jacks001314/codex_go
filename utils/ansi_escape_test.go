package utils

import "testing"

func TestExpandTabs(t *testing.T) {
	if got := ANSIExpandTabs("1\tname"); got != "1    name" {
		t.Fatalf("ANSIExpandTabs() = %q", got)
	}
}

func TestStripCSIAndOSC(t *testing.T) {
	got := StripANSI("\x1b[31mred\x1b[0m \x1b]0;title\x07plain")
	if got != "red plain" {
		t.Fatalf("StripANSI() = %q", got)
	}
}

func TestStripOSCWithStringTerminator(t *testing.T) {
	got := StripANSI("\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\")
	if got != "link" {
		t.Fatalf("StripANSI() = %q", got)
	}
}

func TestStripIgnoresIncompleteEscape(t *testing.T) {
	if got := StripANSI("ok\x1b"); got != "ok" {
		t.Fatalf("StripANSI() = %q", got)
	}
	if got := StripANSI("ok\x1b[31"); got != "ok" {
		t.Fatalf("StripANSI() = %q", got)
	}
}

func TestLineReturnsFirstLine(t *testing.T) {
	if got := ANSIFirstLine("\x1b[32mok\x1b[0m\r\nsecond"); got != "ok" {
		t.Fatalf("ANSIFirstLine() = %q", got)
	}
}

func TestLineExpandsTabsBeforeSelectingFirstLine(t *testing.T) {
	if got := ANSIFirstLine("1\tname\n2\tother"); got != "1    name" {
		t.Fatalf("ANSIFirstLine() = %q", got)
	}
}
