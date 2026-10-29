package mxc

import (
	"reflect"
	"testing"
)

func TestEnvListOmitsAnEmptyChildEnvironmentLikeRust(t *testing.T) {
	if got := envList(map[string]string{}); got != nil {
		t.Fatalf("envList(empty) = %#v, want nil", got)
	}
	if got := envList(nil); got != nil {
		t.Fatalf("envList(nil) = %#v, want nil", got)
	}
}

func TestEnvListForwardsSortedExplicitEnvironmentLikeRust(t *testing.T) {
	got := envList(map[string]string{"ZED": "2", "ALPHA": "1", "PATH": `C:\Windows`})
	want := []string{"ALPHA=1", `PATH=C:\Windows`, "ZED=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envList() = %#v, want %#v", got, want)
	}
}
