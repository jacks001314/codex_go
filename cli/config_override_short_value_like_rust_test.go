package cli

import "testing"

// TestShortConfigOverrideDropsAttachedEquals pins clap's short-option rule for
// the global `-c`/`--config`: one attached "=" is dropped, so `-c=key=value`
// reaches CliConfigOverrides as `key=value` and `-c=` as an empty value.
func TestShortConfigOverrideDropsAttachedEquals(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{args: []string{"exec", "-c="}, want: []string{""}},
		{args: []string{"exec", "-c==x"}, want: []string{"=x"}},
		{args: []string{"exec", "-c=model=x"}, want: []string{"model=x"}},
		{args: []string{"exec", "-cmodel=x"}, want: []string{"model=x"}},
		{args: []string{"exec", "--config="}, want: []string{""}},
	}
	for _, tc := range cases {
		parsed, err := Parse(tc.args)
		if err != nil {
			t.Fatalf("Parse(%v) error: %v", tc.args, err)
		}
		got := parsed.Exec.ConfigOverrides
		if len(got) != len(tc.want) {
			t.Fatalf("Parse(%v) overrides = %#v, want %#v", tc.args, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("Parse(%v) overrides = %#v, want %#v", tc.args, got, tc.want)
			}
		}
	}
}
