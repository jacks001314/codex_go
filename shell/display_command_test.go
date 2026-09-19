package shell

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// These tests mirror the Rust unit tests in
// codex-rs/shell-command/src/parse_command.rs (mod tests). They pin the display
// command parser that feeds the transcript summary and app-server
// commandActions.

func dRead(cmd, name, path string) DisplayCommand {
	return DisplayCommand{Kind: DisplayCommandRead, Cmd: cmd, Name: name, Path: path}
}

func dList(cmd, path string) DisplayCommand {
	return DisplayCommand{Kind: DisplayCommandListFiles, Cmd: cmd, Path: path}
}

func dSearch(cmd, query, path string) DisplayCommand {
	return DisplayCommand{Kind: DisplayCommandSearch, Cmd: cmd, Query: query, Path: path}
}

func dUnknown(cmd string) DisplayCommand {
	return DisplayCommand{Kind: DisplayCommandUnknown, Cmd: cmd}
}

func dispArgs(values ...string) []string {
	return values
}

func dispSplit(value string) []string {
	return SplitCommandLine(value)
}

func TestParseDisplayCommandsMatchesRustParseCommand(t *testing.T) {
	xargsCommand := `rg -l QkBindingController presentation/src/main/java | xargs perl -pi -e 's/QkBindingController/QkController/g'`
	xargsTokens := dispSplit(xargsCommand)
	unknownPipelineTokens := dispSplit("rg --files | nl -ba | foo")
	pythonWalks := `python -c "import os; print(os.listdir('.'))"`
	python3Walks := `python3 -c "import glob; print(glob.glob('*.rs'))"`
	pythonPlain := `python -c "print('hello')"`

	complexHead := "rg --version && node -v && pnpm -v && rg --files | wc -l && rg --files | head -n 40"

	powershellPath := "powershell.exe"
	if runtime.GOOS == "windows" {
		powershellPath = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	} else {
		powershellPath = "/usr/local/bin/powershell.exe"
	}

	cases := []struct {
		name     string
		command  []string
		expected []DisplayCommand
	}{
		{"git_status_is_unknown", dispArgs("git", "status"), []DisplayCommand{dUnknown("git status")}},
		{"git_grep_and_ls_files", dispSplit("git grep TODO src"), []DisplayCommand{dSearch("git grep TODO src", "TODO", "src")}},
		{"git_grep_files_with_matches", dispSplit("git grep -l TODO src"), []DisplayCommand{dSearch("git grep -l TODO src", "TODO", "src")}},
		{"git_ls_files", dispSplit("git ls-files"), []DisplayCommand{dList("git ls-files", "")}},
		{"git_ls_files_path", dispSplit("git ls-files src"), []DisplayCommand{dList("git ls-files src", "src")}},
		{"git_ls_files_exclude", dispSplit("git ls-files --exclude target src"), []DisplayCommand{dList("git ls-files --exclude target src", "src")}},
		{"handles_git_pipe_wc", dispArgs("bash", "-lc", "git status | wc -l"), []DisplayCommand{dUnknown("git status | wc -l")}},
		{"bash_lc_redirect_not_quoted", dispArgs("bash", "-lc", "echo foo > bar"), []DisplayCommand{dUnknown("echo foo > bar")}},
		{"handles_complex_bash_command_head", dispArgs("bash", "-lc", complexHead), []DisplayCommand{dUnknown(complexHead)}},
		{"supports_searching_for_navigate_to_route", dispArgs("bash", "-lc", `rg -n "navigate-to-route" -S`), []DisplayCommand{dSearch("rg -n navigate-to-route -S", "navigate-to-route", "")}},
		{"handles_complex_bash_command", dispArgs("bash", "-lc", `rg -n "BUG|FIXME|TODO|XXX|HACK" -S | head -n 200`), []DisplayCommand{dSearch("rg -n 'BUG|FIXME|TODO|XXX|HACK' -S", "BUG|FIXME|TODO|XXX|HACK", "")}},
		{"supports_rg_files_with_path_and_pipe", dispArgs("bash", "-lc", "rg --files webview/src | sed -n"), []DisplayCommand{dList("rg --files webview/src", "webview")}},
		{"supports_rg_files_then_head", dispArgs("bash", "-lc", "rg --files | head -n 50"), []DisplayCommand{dList("rg --files", "")}},
		{"keeps_mutating_xargs_pipeline", dispArgs("bash", "-lc", xargsCommand), []DisplayCommand{dUnknown(xargsCommand)}},
		{"collapses_plain_pipeline_when_any_stage_is_unknown", xargsTokens, []DisplayCommand{dUnknown(ShlexJoin(xargsTokens))}},
		{"collapses_pipeline_with_helper_when_later_stage_is_unknown", unknownPipelineTokens, []DisplayCommand{dUnknown(ShlexJoin(unknownPipelineTokens))}},
		{"rg_files_with_matches_flags_are_search_l", dispSplit("rg -l TODO src"), []DisplayCommand{dSearch("rg -l TODO src", "TODO", "src")}},
		{"rg_files_with_matches_flags_are_search_long", dispSplit("rg --files-with-matches TODO src"), []DisplayCommand{dSearch("rg --files-with-matches TODO src", "TODO", "src")}},
		{"rg_files_without_match_flags_are_search_L", dispSplit("rg -L TODO src"), []DisplayCommand{dSearch("rg -L TODO src", "TODO", "src")}},
		{"rg_files_without_match_flags_are_search_long", dispSplit("rg --files-without-match TODO src"), []DisplayCommand{dSearch("rg --files-without-match TODO src", "TODO", "src")}},
		{"rga_files_with_matches_flags_are_search", dispSplit("rga -l TODO src"), []DisplayCommand{dSearch("rga -l TODO src", "TODO", "src")}},
		{"supports_cat", dispArgs("bash", "-lc", "cat webview/README.md"), []DisplayCommand{dRead("cat webview/README.md", "README.md", "webview/README.md")}},
		{"zsh_lc_supports_cat", dispArgs("zsh", "-lc", "cat README.md"), []DisplayCommand{dRead("cat README.md", "README.md", "README.md")}},
		{"supports_bat", dispArgs("bash", "-lc", "bat --theme TwoDark README.md"), []DisplayCommand{dRead("bat --theme TwoDark README.md", "README.md", "README.md")}},
		{"supports_batcat", dispArgs("bash", "-lc", "batcat README.md"), []DisplayCommand{dRead("batcat README.md", "README.md", "README.md")}},
		{"supports_less", dispArgs("bash", "-lc", "less -p TODO README.md"), []DisplayCommand{dRead("less -p TODO README.md", "README.md", "README.md")}},
		{"supports_more", dispArgs("bash", "-lc", "more README.md"), []DisplayCommand{dRead("more README.md", "README.md", "README.md")}},
		{"cd_then_cat_is_single_read", dispSplit("cd foo && cat foo.txt"), []DisplayCommand{dRead("cat foo.txt", "foo.txt", filepath.Join("foo", "foo.txt"))}},
		{"cd_with_double_dash_then_cat_is_read", dispSplit("cd -- -weird && cat foo.txt"), []DisplayCommand{dRead("cat foo.txt", "foo.txt", filepath.Join("-weird", "foo.txt"))}},
		{"cd_with_multiple_operands_uses_last", dispSplit("cd dir1 dir2 && cat foo.txt"), []DisplayCommand{dRead("cat foo.txt", "foo.txt", filepath.Join("dir2", "foo.txt"))}},
		{"bash_cd_then_bar_is_same_as_bar", dispSplit("bash -lc 'cd foo && bar'"), []DisplayCommand{dUnknown("cd foo && bar")}},
		{"bash_cd_then_cat_is_read", dispSplit("bash -lc 'cd foo && cat foo.txt'"), []DisplayCommand{dRead("cat foo.txt", "foo.txt", filepath.Join("foo", "foo.txt"))}},
		{"supports_ls_with_pipe", dispArgs("bash", "-lc", "ls -la | sed -n '1,120p'"), []DisplayCommand{dList("ls -la", "")}},
		{"supports_eza", dispSplit("eza --color=always src"), []DisplayCommand{dList("eza '--color=always' src", "src")}},
		{"supports_exa", dispSplit("exa -I target ."), []DisplayCommand{dList("exa -I target .", ".")}},
		{"supports_tree", dispSplit("tree -L 2 src"), []DisplayCommand{dList("tree -L 2 src", "src")}},
		{"supports_du", dispSplit("du -d 2 ."), []DisplayCommand{dList("du -d 2 .", ".")}},
		{"supports_head_n", dispArgs("bash", "-lc", "head -n 50 Cargo.toml"), []DisplayCommand{dRead("head -n 50 Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"supports_head_file_only", dispArgs("bash", "-lc", "head Cargo.toml"), []DisplayCommand{dRead("head Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"supports_cat_sed_n", dispArgs("bash", "-lc", "cat tui/Cargo.toml | sed -n '1,200p'"), []DisplayCommand{dRead("cat tui/Cargo.toml | sed -n '1,200p'", "Cargo.toml", "tui/Cargo.toml")}},
		{"supports_tail_n_plus", dispArgs("bash", "-lc", "tail -n +522 README.md"), []DisplayCommand{dRead("tail -n +522 README.md", "README.md", "README.md")}},
		{"supports_tail_n_last_lines", dispArgs("bash", "-lc", "tail -n 30 README.md"), []DisplayCommand{dRead("tail -n 30 README.md", "README.md", "README.md")}},
		{"supports_tail_file_only", dispArgs("bash", "-lc", "tail README.md"), []DisplayCommand{dRead("tail README.md", "README.md", "README.md")}},
		{"supports_npm_run_build_is_unknown", dispArgs("npm", "run", "build"), []DisplayCommand{dUnknown("npm run build")}},
		{"supports_grep_recursive_current_dir", dispArgs("grep", "-R", "CODEX_SANDBOX_ENV_VAR", "-n", "."), []DisplayCommand{dSearch("grep -R CODEX_SANDBOX_ENV_VAR -n .", "CODEX_SANDBOX_ENV_VAR", ".")}},
		{"supports_grep_recursive_specific_file", dispArgs("grep", "-R", "CODEX_SANDBOX_ENV_VAR", "-n", "core/src/spawn.rs"), []DisplayCommand{dSearch("grep -R CODEX_SANDBOX_ENV_VAR -n core/src/spawn.rs", "CODEX_SANDBOX_ENV_VAR", "spawn.rs")}},
		{"supports_egrep", dispSplit("egrep -R TODO src"), []DisplayCommand{dSearch("egrep -R TODO src", "TODO", "src")}},
		{"supports_fgrep", dispSplit("fgrep -l TODO src"), []DisplayCommand{dSearch("fgrep -l TODO src", "TODO", "src")}},
		{"grep_files_with_matches_l", dispSplit("grep -l TODO src"), []DisplayCommand{dSearch("grep -l TODO src", "TODO", "src")}},
		{"grep_files_with_matches_long", dispSplit("grep --files-with-matches TODO src"), []DisplayCommand{dSearch("grep --files-with-matches TODO src", "TODO", "src")}},
		{"grep_files_without_match_L", dispSplit("grep -L TODO src"), []DisplayCommand{dSearch("grep -L TODO src", "TODO", "src")}},
		{"grep_files_without_match_long", dispSplit("grep --files-without-match TODO src"), []DisplayCommand{dSearch("grep --files-without-match TODO src", "TODO", "src")}},
		{"supports_grep_query_with_slashes_not_shortened", dispSplit("grep -R src/main.rs -n ."), []DisplayCommand{dSearch("grep -R src/main.rs -n .", "src/main.rs", ".")}},
		{"supports_grep_weird_backtick_in_query", dispSplit("grep -R COD`EX_SANDBOX -n"), []DisplayCommand{dSearch("grep -R 'COD`EX_SANDBOX' -n", "COD`EX_SANDBOX", "")}},
		{"supports_cd_and_rg_files", dispSplit("cd codex-rs && rg --files"), []DisplayCommand{dList("rg --files", "")}},
		{"supports_single_string_script_with_cd_and_pipe", dispArgs("bash", "-lc", `cd /Users/pakrym/code/codex && rg -n "codex_api" codex-rs -S | head -n 50`), []DisplayCommand{dSearch("rg -n codex_api codex-rs -S", "codex_api", "codex-rs")}},
		{"supports_python_walks_files", dispArgs("bash", "-lc", pythonWalks), []DisplayCommand{dList(ShlexJoin(dispSplit(pythonWalks)), "")}},
		{"supports_python3_walks_files", dispArgs("bash", "-lc", python3Walks), []DisplayCommand{dList(ShlexJoin(dispSplit(python3Walks)), "")}},
		{"python_without_file_walk_is_unknown", dispArgs("bash", "-lc", pythonPlain), []DisplayCommand{dUnknown(ShlexJoin(dispSplit(pythonPlain)))}},
		{"supports_sed_n_then_nl_as_search", dispSplit("sed -n '260,640p' exec/src/event_processor_with_human_output.rs | nl -ba"), []DisplayCommand{dRead("sed -n '260,640p' exec/src/event_processor_with_human_output.rs", "event_processor_with_human_output.rs", "exec/src/event_processor_with_human_output.rs")}},
		{"supports_nl_then_sed_reading", dispArgs("bash", "-lc", "nl -ba core/src/parse_command.rs | sed -n '1200,1720p'"), []DisplayCommand{dRead("nl -ba core/src/parse_command.rs | sed -n '1200,1720p'", "parse_command.rs", "core/src/parse_command.rs")}},
		{"supports_sed_n", dispArgs("bash", "-lc", "sed -n '2000,2200p' tui/src/history_cell.rs"), []DisplayCommand{dRead("sed -n '2000,2200p' tui/src/history_cell.rs", "history_cell.rs", "tui/src/history_cell.rs")}},
		{"supports_awk_with_file", dispArgs("bash", "-lc", "awk '{print $1}' Cargo.toml"), []DisplayCommand{dRead("awk '{print $1}' Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"filters_out_printf", dispArgs("bash", "-lc", "printf \"\\n===== ansi-escape/Cargo.toml =====\\n\"; cat -- ansi-escape/Cargo.toml"), []DisplayCommand{dRead("cat -- ansi-escape/Cargo.toml", "Cargo.toml", "ansi-escape/Cargo.toml")}},
		{"drops_yes_in_pipelines", dispArgs("bash", "-lc", "yes | rg --files"), []DisplayCommand{dList("rg --files", "")}},
		{"preserves_rg_with_spaces", dispSplit("yes | rg -n 'foo bar' -S"), []DisplayCommand{dSearch("rg -n 'foo bar' -S", "foo bar", "")}},
		{"ls_with_glob", dispSplit("ls -I '*.test.js'"), []DisplayCommand{dList("ls -I '*.test.js'", "")}},
		{"strips_true_in_sequence_prefix", dispSplit("true && rg --files"), []DisplayCommand{dList("rg --files", "")}},
		{"strips_true_in_sequence_suffix", dispSplit("rg --files && true"), []DisplayCommand{dList("rg --files", "")}},
		{"strips_true_inside_bash_lc_prefix", dispArgs("bash", "-lc", "true && rg --files"), []DisplayCommand{dList("rg --files", "")}},
		{"strips_true_inside_bash_lc_suffix", dispArgs("bash", "-lc", "rg --files || true"), []DisplayCommand{dList("rg --files", "")}},
		{"shorten_path_on_windows", dispSplit(`cat "pkg\src\main.rs"`), []DisplayCommand{dRead("cat \"pkg\\\\src\\\\main.rs\"", "main.rs", `pkg\src\main.rs`)}},
		{"head_with_no_space", dispSplit("bash -lc 'head -n50 Cargo.toml'"), []DisplayCommand{dRead("head -n50 Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"bash_dash_c_pipeline_parsing", dispArgs("bash", "-c", "rg --files | head -n 1"), []DisplayCommand{dList("rg --files", "")}},
		{"tail_with_no_space", dispSplit("bash -lc 'tail -n+10 README.md'"), []DisplayCommand{dRead("tail -n+10 README.md", "README.md", "README.md")}},
		{"grep_with_query_and_path", dispSplit("grep -R TODO src"), []DisplayCommand{dSearch("grep -R TODO src", "TODO", "src")}},
		{"supports_ag", dispSplit("ag TODO src"), []DisplayCommand{dSearch("ag TODO src", "TODO", "src")}},
		{"supports_ack", dispSplit("ack TODO src"), []DisplayCommand{dSearch("ack TODO src", "TODO", "src")}},
		{"supports_pt", dispSplit("pt TODO src"), []DisplayCommand{dSearch("pt TODO src", "TODO", "src")}},
		{"supports_rga", dispSplit("rga TODO src"), []DisplayCommand{dSearch("rga TODO src", "TODO", "src")}},
		{"ag_files_with_matches", dispSplit("ag -l TODO src"), []DisplayCommand{dSearch("ag -l TODO src", "TODO", "src")}},
		{"ack_files_with_matches", dispSplit("ack -l TODO src"), []DisplayCommand{dSearch("ack -l TODO src", "TODO", "src")}},
		{"pt_files_with_matches", dispSplit("pt -l TODO src"), []DisplayCommand{dSearch("pt -l TODO src", "TODO", "src")}},
		{"rg_with_equals_style_flags", dispSplit("rg --colors=never -n foo src"), []DisplayCommand{dSearch("rg '--colors=never' -n foo src", "foo", "src")}},
		{"cat_with_double_dash", dispSplit("cat -- ./-strange-file-name"), []DisplayCommand{dRead("cat -- ./-strange-file-name", "-strange-file-name", "./-strange-file-name")}},
		{"sed_with_range_and_file", dispSplit("sed -n '12,20p' Cargo.toml"), []DisplayCommand{dRead("sed -n '12,20p' Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"drop_trailing_nl_in_pipeline", dispSplit("rg --files | nl -ba"), []DisplayCommand{dList("rg --files", "")}},
		{"ls_with_time_style_and_path", dispSplit("ls --time-style=long-iso ./dist"), []DisplayCommand{dList("ls '--time-style=long-iso' ./dist", ".")}},
		{"fd_type_only_path", dispSplit("fd -t f src/"), []DisplayCommand{dList("fd -t f src/", "src")}},
		{"fd_query_and_path", dispSplit("fd main src"), []DisplayCommand{dSearch("fd main src", "main", "src")}},
		{"find_basic_name_filter", dispSplit("find . -name '*.rs'"), []DisplayCommand{dSearch("find . -name '*.rs'", "*.rs", ".")}},
		{"find_type_only_path", dispSplit("find src -type f"), []DisplayCommand{dList("find src -type f", "src")}},
		{"bin_bash_lc_sed", dispSplit("/bin/bash -lc 'sed -n '1,10p' Cargo.toml'"), []DisplayCommand{dRead("sed -n '1,10p' Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"bin_zsh_lc_sed", dispSplit("/bin/zsh -lc 'sed -n '1,10p' Cargo.toml'"), []DisplayCommand{dRead("sed -n '1,10p' Cargo.toml", "Cargo.toml", "Cargo.toml")}},
		{"powershell_command_is_stripped", dispArgs("powershell", "-Command", "Get-ChildItem"), []DisplayCommand{dUnknown("Get-ChildItem")}},
		{"pwsh_with_noprofile_and_c_alias_is_stripped", dispArgs("pwsh", "-NoProfile", "-c", "Write-Host hi"), []DisplayCommand{dUnknown("Write-Host hi")}},
		{"powershell_with_path_is_stripped", dispArgs(powershellPath, "-NoProfile", "-c", "Write-Host hi"), []DisplayCommand{dUnknown("Write-Host hi")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDisplayCommands(tc.command)
			if len(got) == 0 && len(tc.expected) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("ParseDisplayCommands(%q) = %#v, want %#v", tc.command, got, tc.expected)
			}
		})
	}
}

func TestParseDisplayCommandsPowerShellReadsLikeRust(t *testing.T) {
	cases := []struct {
		shell  string
		script string
		path   string
	}{
		{"powershell", `Get-Content C:\skills\demo\SKILL.md`, "C:/skills/demo/SKILL.md"},
		{"powershell", `get-content -Raw "C:\skills and plugins\SKILL.md"`, "C:/skills and plugins/SKILL.md"},
		{"powershell", `Get-Content 'C:\skills and plugins\SKILL.md'`, "C:/skills and plugins/SKILL.md"},
		{"powershell", `Get-Content -Path C:\skills\demo\SKILL.md`, "C:/skills/demo/SKILL.md"},
		{"powershell", `Get-Content -LiteralPath C:\skills\demo\SKILL.md`, "C:/skills/demo/SKILL.md"},
		{"powershell", `Get-Content C:\skills\demo\SKILL.md -Raw`, "C:/skills/demo/SKILL.md"},
		{"powershell", `Get-Content -Raw -LiteralPath C:\skills\demo\SKILL.md`, "C:/skills/demo/SKILL.md"},
		{"powershell", `Get-Content C:\workspace\README.md`, "C:/workspace/README.md"},
		{"powershell", "gc C:/skills/demo/SKILL.md", "C:/skills/demo/SKILL.md"},
		{"powershell", "type C:/skills/demo/SKILL.md", "C:/skills/demo/SKILL.md"},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "Get-Content C:/skills/demo/SKILL.md", "C:/skills/demo/SKILL.md"},
	}
	for _, tc := range cases {
		expected := []DisplayCommand{dRead(tc.script, baseNameForTest(tc.path), tc.path)}
		got := ParseDisplayCommands([]string{tc.shell, "-NoProfile", "-Command", tc.script})
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("ParseDisplayCommands(%q) = %#v, want %#v", tc.script, got, expected)
		}
	}
}

func TestParseDisplayCommandsComplexPowerShellReadsAreUnknownLikeRust(t *testing.T) {
	for _, script := range []string{
		`Get-Content 'C:\Users\O''Brien\skill\SKILL.md'`,
		`Get-Content "$(Remove-Item C:/important)/skills/demo/SKILL.md"`,
		"Get-Content -ReadCount:([IO.File]::Delete('C:/important')) C:/skills/demo/SKILL.md",
		"Get-Content C:/Users/Alice/.ssh/id_rsa,C:/skills/demo/SKILL.md",
		"Get-Content C:/skills/demo/SKILL.md -Raw; Remove-Item C:/important",
		"Get-Content C:/skills/demo/SKILL.md C:/important",
		"Get-Content C:/skills/*/SKILL.md",
		"Get-Content -Encoding UTF8 C:/skills/demo/SKILL.md",
		"Get-Content -Raw",
	} {
		expected := []DisplayCommand{dUnknown(script)}
		got := ParseDisplayCommands([]string{"powershell", "-Command", script})
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("ParseDisplayCommands(%q) = %#v, want %#v", script, got, expected)
		}
	}
}

func TestParseDisplayCommandsKeepsMutatingSedInCompoundCommandLikeRust(t *testing.T) {
	for _, sedCommand := range []string{
		"sed -n -i.bak 1p secret.txt",
		"sed -ni.bak 1p secret.txt",
		"sed -Eni.bak 1p secret.txt",
	} {
		inner := "cat README.md && " + sedCommand
		expected := []DisplayCommand{dUnknown(inner)}
		got := ParseDisplayCommands([]string{"bash", "-lc", inner})
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("ParseDisplayCommands(%q) = %#v, want %#v", inner, got, expected)
		}
	}
}

func TestParseDisplayCommandsIgnoresSedOperandsAfterDoubleDashLikeRust(t *testing.T) {
	got := ParseDisplayCommands([]string{"bash", "-lc", "cat README.md && sed 's/a/x/' -- -input.txt"})
	expected := []DisplayCommand{dRead("cat README.md", "README.md", "README.md")}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("ParseDisplayCommands() = %#v, want %#v", got, expected)
	}
}

func TestIsSmallFormattingCommandMatchesRust(t *testing.T) {
	for _, cmd := range []string{"wc", "tr", "cut", "sort", "uniq", "xargs", "tee", "column"} {
		if !isSmallFormattingCommand(dispSplit(cmd)) {
			t.Fatalf("isSmallFormattingCommand(%q) = false, want true", cmd)
		}
		if !isSmallFormattingCommand(dispSplit(cmd + " -x")) {
			t.Fatalf("isSmallFormattingCommand(%q -x) = false, want true", cmd)
		}
	}
	if isSmallFormattingCommand(nil) {
		t.Fatal("isSmallFormattingCommand(nil) = true, want false")
	}

	smallness := []struct {
		command string
		small   bool
	}{
		{`awk '{print $1}'`, true},
		{`awk '{print $1}' Cargo.toml`, false},
		{"awk -f script.awk Cargo.toml", false},
		{"head", true},
		{"head -n 40", true},
		{"head -n 40 file.txt", false},
		{"head file.txt", false},
		{"tail", true},
		{"tail -n +10", true},
		{"tail -n +10 file.txt", false},
		{"tail -n 30", true},
		{"tail -n 30 file.txt", false},
		{"tail -c 30", true},
		{"tail -c +10", true},
		{"tail file.txt", false},
		{"sed", true},
		{"sed -n 10p", true},
		{"sed -n 10p file.txt", false},
		{"sed -n -e 10p file.txt", false},
		{"sed -n 10p -- file.txt", false},
		{"sed -n 1,200p file.txt", false},
		{"sed -n p file.txt", true},
		{"sed -n +10p file.txt", true},
	}
	for _, tc := range smallness {
		got := isSmallFormattingCommand(dispSplit(tc.command))
		if got != tc.small {
			t.Fatalf("isSmallFormattingCommand(%q) = %v, want %v", tc.command, got, tc.small)
		}
	}
}

func baseNameForTest(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i] {
		case '/', '\\':
			return path[i+1:]
		}
	}
	return path
}
