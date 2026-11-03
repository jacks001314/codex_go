package shellparse

import "testing"

// TestParsePlainPowerShellCommandsMatchesRustFixtures replays the lowerings
// recorded in Rust's fixtures/powershell_lowering.json.
func TestParsePlainPowerShellCommandsMatchesRustFixtures(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   [][]string
	}{
		{name: "echo", script: "echo hi", want: [][]string{{"echo", "hi"}}},
		{name: "single_quote", script: "Get-Content 'foo bar'", want: [][]string{{"Get-Content", "foo bar"}}},
		{name: "quoted_comma", script: "Get-Content 'foo,bar'", want: [][]string{{"Get-Content", "foo,bar"}}},
		{name: "double_quote", script: "Get-Content \"foo bar\"", want: [][]string{{"Get-Content", "foo bar"}}},
		{name: "doubled_single_quote", script: "Write-Output 'a''b'", want: [][]string{{"Write-Output", "a'b"}}},
		{name: "escaped_double_quote", script: "Write-Output \"a`\"b\"", want: [][]string{{"Write-Output", "a\"b"}}},
		{name: "escaped_space", script: "Get-Content foo` bar", want: [][]string{{"Get-Content", "foo bar"}}},
		{name: "command_parameter", script: "Get-ChildItem -Path .", want: [][]string{{"Get-ChildItem", "-Path", "."}}},
		{name: "pipeline", script: "Write-Output foo | Measure-Object", want: [][]string{{"Write-Output", "foo"}, {"Measure-Object"}}},
		{name: "semicolon", script: "Get-Content Cargo.toml; Test-Path Cargo.toml", want: [][]string{{"Get-Content", "Cargo.toml"}, {"Test-Path", "Cargo.toml"}}},
		{name: "and_chain", script: "pwd && ls", want: [][]string{{"pwd"}, {"ls"}}},
		{name: "or_chain", script: "pwd || ls", want: [][]string{{"pwd"}, {"ls"}}},
		{name: "canonical_numeric_argument", script: "Get-Content -TotalCount 100", want: [][]string{{"Get-Content", "-TotalCount", "100"}}},
		{name: "negative_numeric_argument", script: "git log -1 Cargo.toml", want: [][]string{{"git", "log", "-1", "Cargo.toml"}}},
		{name: "inline_double_dash_parameter", script: "Get-Content --flag=value", want: [][]string{{"Get-Content", "--flag=value"}}},
		{name: "inline_double_dash_parameter_pipeline", script: "Get-Content --flag=value | Measure-Object", want: [][]string{{"Get-Content", "--flag=value"}, {"Measure-Object"}}},
		{name: "git_dir_inline", script: "git --git-dir=.git status", want: [][]string{{"git", "--git-dir=.git", "status"}}},
		{name: "windows_path", script: `Get-Content C:\tmp\x`, want: [][]string{{"Get-Content", `C:\tmp\x`}}},
		{name: "dangerous_literal_url", script: "Start-Process 'https://example.com'", want: [][]string{{"Start-Process", "https://example.com"}}},
		{name: "dangerous_literal_force", script: "Remove-Item test -Force", want: [][]string{{"Remove-Item", "test", "-Force"}}},
		{name: "trailing_comment", script: "Get-Content Cargo.toml # comment", want: [][]string{{"Get-Content", "Cargo.toml"}}},
		{name: "hash_inside_token", script: "Get-Content foo#bar", want: [][]string{{"Get-Content", "foo#bar"}}},
		{name: "hash_inside_recovered_command_token", script: "pwd#evil --flag=value", want: [][]string{{"pwd#evil", "--flag=value"}}},
		{name: "block_comment_before_command", script: "<# note #>\nGet-Content foo", want: [][]string{{"Get-Content", "foo"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ParsePlainPowerShellCommands(test.script)
			if !ok {
				t.Fatalf("ParsePlainPowerShellCommands(%q) failed", test.script)
			}
			if len(got) != len(test.want) {
				t.Fatalf("ParsePlainPowerShellCommands(%q) = %#v, want %#v", test.script, got, test.want)
			}
			for index := range test.want {
				if len(got[index]) != len(test.want[index]) {
					t.Fatalf("ParsePlainPowerShellCommands(%q) = %#v, want %#v", test.script, got, test.want)
				}
				for word := range test.want[index] {
					if got[index][word] != test.want[index][word] {
						t.Fatalf("ParsePlainPowerShellCommands(%q) = %#v, want %#v", test.script, got, test.want)
					}
				}
			}
		})
	}
}

// TestParsePlainPowerShellCommandsRejectsDynamicSyntax keeps the lowering
// fail-closed for every shape Rust leaves opaque.
func TestParsePlainPowerShellCommandsRejectsDynamicSyntax(t *testing.T) {
	cases := []string{
		"rg \"--pr`e=helper.exe\" pattern",
		"rg \"--pre=`u{68}elper.exe\" pattern",
		"Get-Content -Path:1",
		"Get-Content -Path:'foo bar'",
		"(Get-Content foo.rs -Raw)",
		"(pwd && ls)",
		"Write-Output 0x10",
		"Write-Output 01",
		"Write-Output 1.0",
		"Write-Output 1e3",
		"Get-Content 1.0.0",
		"Write-Output @'\nhello\n'@",
		"Remove-Item test \u2013Force",
		"git status --short#; Remove-Item victim",
		"echo hi > out.txt",
		"& Remove-Item foo",
		"Write-Output (Set-Content foo6.txt 'abc')",
		"Write-Output $(Get-Content foo)",
		"ls @(calc.exe)",
		"Get-Content @args",
		"''",
		"Get-Content $foo",
		"Write-Output \"foo $bar\"",
		"Get-Content foo,bar",
		"Get-Content -foo=bar",
		"Get-Content --flag=value )",
		"Get-Content --flag=value | if (",
		"Get-Content --flag=value `",
		"#requires -Modules Evil\nGet-Location",
		"param([string]$path = (Get-Location)) Write-Output test",
		"begin { Set-Content codex_poc.txt pwned } end { Get-Content Cargo.toml }",
		"trap { Set-Content codex_poc.txt pwned; continue } Get-Content missing",
	}
	for _, script := range cases {
		if commands, ok := ParsePlainPowerShellCommands(script); ok {
			t.Fatalf("ParsePlainPowerShellCommands(%q) = %#v, want rejection", script, commands)
		}
	}
}
