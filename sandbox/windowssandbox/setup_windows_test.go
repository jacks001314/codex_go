//go:build windows

package windowssandbox

import (
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
