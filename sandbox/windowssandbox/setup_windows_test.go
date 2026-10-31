//go:build windows

package windowssandbox

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSetupExecutableArgs(t *testing.T) {
	tests := []struct {
		name            string
		useDispatchFlag bool
		want            []string
	}{
		{name: "dedicated helper", want: []string{"payload"}},
		{name: "main executable fallback", useDispatchFlag: true, want: []string{windowsSandboxSetupFlag, "payload"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := setupExecutableArgs("payload", test.useDispatchFlag); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("setupExecutableArgs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSetupPayloadCarriesUserProfileLikeRust(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\example`)
	if got := setupUserProfile(); got != `C:\Users\example` {
		t.Fatalf("setupUserProfile() = %q", got)
	}
	t.Setenv("USERPROFILE", "  ")
	if got := setupUserProfile(); got != "" {
		t.Fatalf("setupUserProfile() with blank env = %q, want empty", got)
	}

	data, err := json.Marshal(setupElevationPayload{UserProfile: `C:\Users\example`})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["user_profile"] != `C:\Users\example` {
		t.Fatalf("user_profile = %#v, want passthrough", decoded["user_profile"])
	}

	data, err = json.Marshal(setupElevationPayload{})
	if err != nil {
		t.Fatalf("Marshal(empty) error = %v", err)
	}
	decoded = nil
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(empty) error = %v", err)
	}
	if _, ok := decoded["user_profile"]; ok {
		t.Fatal("empty user_profile must stay omitted")
	}
}
