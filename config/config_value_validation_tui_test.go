package config

import "testing"

// The TUI table mixes named fields with `#[serde(flatten)]` notification
// settings, so the flatten failures are reported against `tui` itself and only
// after the named fields.
func TestValidateTuiConfigTable(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "notifications are an untagged enum",
			toml: "[tui]\nnotifications = 5\n",
			want: "data did not match any variant of untagged enum Notifications\nin `tui`",
		},
		{
			name: "notification strings are not a variant",
			toml: "[tui]\nnotifications = \"off\"\n",
			want: "data did not match any variant of untagged enum Notifications\nin `tui`",
		},
		{
			name: "notification booleans are accepted",
			toml: "[tui]\nnotifications = false\n",
			want: "",
		},
		{
			name: "notification method lists variants at the table path",
			toml: "[tui]\nnotification_method = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `auto`, `osc9`, `osc777`, `bel`\nin `tui`",
		},
		{
			name: "notification method type",
			toml: "[tui]\nnotification_method = 5\n",
			want: "invalid type: integer `5`, expected string or map\nin `tui`",
		},
		{
			name: "notification method empty map",
			toml: "[tui]\nnotification_method = {}\n",
			want: "invalid value: map, expected map with a single key\nin `tui`",
		},
		{
			name: "notification condition is a two-variant enum",
			toml: "[tui]\nnotification_condition = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `unfocused` or `always`\nin `tui`",
		},
		{
			name: "named fields are checked before flattened ones",
			toml: "[tui]\nnotifications = 5\n[tui.effects]\nstarfield = 5\n",
			want: "invalid type: integer `5`, expected a boolean\nin `tui.effects.starfield`",
		},
		{
			name: "copy on select is an enum",
			toml: "[tui]\ncopy_on_select = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `auto`, `always`, `never`\nin `tui.copy_on_select`",
		},
		{
			name: "status line entries are strings",
			toml: "[tui]\nstatus_line = [1]\n",
			want: "invalid type: integer `1`, expected a string\nin `tui.status_line`",
		},
		{
			name: "mouse scroll speed is a float",
			toml: "[tui]\nmouse_scroll_speed = \"x\"\n",
			want: "invalid type: string \"x\", expected f64\nin `tui.mouse_scroll_speed`",
		},
		{
			name: "effects is a struct",
			toml: "[tui]\neffects = 5\n",
			want: "invalid type: integer `5`, expected struct TuiEffects\nin `tui.effects`",
		},
		{
			name: "presets require an input",
			toml: "[tui.presets.\"0\"]\nlabel = \"hi\"\n",
			want: "missing field `input`\nin `tui.presets.0`",
		},
		{
			name: "presets entries are QuickDial structs",
			toml: "[tui]\npresets = { \"0\" = 5 }\n",
			want: "invalid type: integer `5`, expected struct QuickDial\nin `tui.presets.0`",
		},
		{
			name: "unknown tui keys stay allowed",
			toml: "[tui]\nunknown = 1\n",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateConfigValues(values)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
