package apps

import (
	"reflect"
	"testing"
)

// TestAppsConfigParsesConnectorToolExposureOmissions mirrors Rust #46035's
// config coverage: the connector's omit_tools_from list is parsed from either
// spelling, an absent key leaves lower-priority configuration unchanged, and an
// explicit empty list is preserved so it can clear connector-level omissions.
func TestAppsConfigParsesConnectorToolExposureOmissions(t *testing.T) {
	populated := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{
			"calendar": map[string]any{"omit_tools_from": []any{"deferred"}},
			"mail":     map[string]any{"omitToolsFrom": []any{"code_mode", "direct"}},
		},
	})
	if populated == nil {
		t.Fatal("AppsConfigFromValues() = nil")
	}
	calendar, ok := populated.Apps["calendar"]
	if !ok || calendar.OmitToolsFrom == nil || !reflect.DeepEqual(*calendar.OmitToolsFrom, []string{"deferred"}) {
		t.Fatalf("calendar omit_tools_from = %#v", calendar.OmitToolsFrom)
	}
	mail, ok := populated.Apps["mail"]
	if !ok || mail.OmitToolsFrom == nil || !reflect.DeepEqual(*mail.OmitToolsFrom, []string{"code_mode", "direct"}) {
		t.Fatalf("mail omit_tools_from = %#v", mail.OmitToolsFrom)
	}

	empty := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{"calendar": map[string]any{"omit_tools_from": []any{}}},
	})
	if empty == nil {
		t.Fatal("AppsConfigFromValues(empty) = nil")
	}
	emptyCalendar := empty.Apps["calendar"]
	if emptyCalendar.OmitToolsFrom == nil || len(*emptyCalendar.OmitToolsFrom) != 0 {
		t.Fatalf("empty omit_tools_from = %#v", emptyCalendar.OmitToolsFrom)
	}

	absent := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{"calendar": map[string]any{"enabled": true}},
	})
	if absent == nil {
		t.Fatal("AppsConfigFromValues(absent) = nil")
	}
	if absent.Apps["calendar"].OmitToolsFrom != nil {
		t.Fatalf("absent omit_tools_from = %#v, want nil", absent.Apps["calendar"].OmitToolsFrom)
	}

	// An unknown surface is dropped, and a table whose only setting is the
	// omitted list still counts as configured.
	unknown := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{"calendar": map[string]any{"omit_tools_from": []any{"deferred", "unsupported"}}},
	})
	if unknown == nil {
		t.Fatal("AppsConfigFromValues(unknown-only table) = nil")
	}
	if got := unknown.Apps["calendar"].OmitToolsFrom; got == nil || !reflect.DeepEqual(*got, []string{"deferred"}) {
		t.Fatalf("unknown surface handling = %#v", got)
	}
}
