package cli

import "testing"

// TestSubcommandGroupFlagDiagnosticsLikeRust pins clap's unknown-argument
// diagnostics for the command groups whose long-flag tables were added in
// round 32: the did-you-mean tip, the `--` workaround tip on commands with
// positionals, and the smart usage block (required options first, then the
// options clap already accepted, then the positionals).
func TestSubcommandGroupFlagDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{
			name:    "mcp add --urll http://x",
			args:    []string{"mcp", "add", "--urll", "http://x"},
			message: "unexpected argument '--urll' found\n\n  tip: a similar argument exists: '--url'\n  tip: to pass '--urll' as a value, use '-- --urll'",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "mcp list --hepl",
			args:    []string{"mcp", "list", "--hepl"},
			message: "unexpected argument '--hepl' found\n\n  tip: a similar argument exists: '--help'",
			usage:   "Usage: codex mcp list --help",
		},
		{
			name:    "mcp get --hepl name",
			args:    []string{"mcp", "get", "--hepl", "name"},
			message: "unexpected argument '--hepl' found\n\n  tip: a similar argument exists: '--help'",
			usage:   "Usage: codex mcp get --help <NAME>",
		},
		{
			name:    "cloud list --limt 5",
			args:    []string{"cloud", "list", "--limt", "5"},
			message: "unexpected argument '--limt' found\n\n  tip: a similar argument exists: '--limit'",
			usage:   "Usage: codex cloud list --limit <N>",
		},
		{
			name:    "cloud status --tas x",
			args:    []string{"cloud", "status", "--tas", "x"},
			message: "unexpected argument '--tas' found\n\n  tip: to pass '--tas' as a value, use '-- --tas'",
			usage:   "Usage: codex cloud status [OPTIONS] <TASK_ID>",
		},
		{
			name:    "debug models --bundld",
			args:    []string{"debug", "models", "--bundld"},
			message: "unexpected argument '--bundld' found\n\n  tip: a similar argument exists: '--bundled'",
			usage:   "Usage: codex debug models --bundled",
		},
		{
			name:    "sandbox --profil p true",
			args:    []string{"sandbox", "--profil", "p", "true"},
			message: "unexpected argument '--profil' found\n\n  tip: a similar argument exists: '--profile'\n  tip: to pass '--profil' as a value, use '-- --profil'",
			usage:   "Usage: codex sandbox --profile <CONFIG_PROFILE> [COMMAND]...",
		},
		{
			name:    "sandbox --bogus true",
			args:    []string{"sandbox", "--bogus", "true"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex sandbox [OPTIONS] [COMMAND]... [COMMAND]",
		},
		{
			name:    "queue --thred t --message m",
			args:    []string{"queue", "--thred", "t", "--message", "m"},
			message: "unexpected argument '--thred' found\n\n  tip: a similar argument exists: '--thread'",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "queue --thread t --bogus",
			args:    []string{"queue", "--thread", "t", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "agents --remot ws://h:1",
			args:    []string{"agents", "--remot", "ws://h:1"},
			message: "unexpected argument '--remot' found\n\n  tip: a similar argument exists: '--remote'",
			usage:   "Usage: codex agents --remote <ADDR>",
		},
		{
			name:    "login --with-api-ke",
			args:    []string{"login", "--with-api-ke"},
			message: "unexpected argument '--with-api-ke' found\n\n  tip: a similar argument exists: '--with-api-key'",
			usage:   "Usage: codex login --with-api-key",
		},
		{
			name:    "app --path x",
			args:    []string{"app", "--path", "x"},
			message: "unexpected argument '--path' found\n\n  tip: to pass '--path' as a value, use '-- --path'",
			usage:   "Usage: codex app [OPTIONS] [PATH]",
		},
		{
			name:    "update --chek",
			args:    []string{"update", "--chek"},
			message: "unexpected argument '--chek' found",
			usage:   "Usage: codex update [OPTIONS]",
		},
		{
			name:    "completion --shel bash",
			args:    []string{"completion", "--shel", "bash"},
			message: "unexpected argument '--shel' found\n\n  tip: a similar argument exists: '--help'",
			usage:   "Usage: codex completion --help [SHELL]",
		},
		{
			name:    "features list --bogus",
			args:    []string{"features", "list", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex features list [OPTIONS]",
		},
		{
			name:    "plugin list --bogus",
			args:    []string{"plugin", "list", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex plugin list [OPTIONS]",
		},
		{
			name:    "plugin list --markeplace x",
			args:    []string{"plugin", "list", "--markeplace", "x"},
			message: "unexpected argument '--markeplace' found\n\n  tip: a similar argument exists: '--marketplace'",
			usage:   "Usage: codex plugin list --marketplace <MARKETPLACE>",
		},
		{
			name:    "doctor --bogus",
			args:    []string{"doctor", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex doctor [OPTIONS]",
		},
		{
			name:    "app-server --lsiten stdio://",
			args:    []string{"app-server", "--lsiten", "stdio://"},
			message: "unexpected argument '--lsiten' found\n\n  tip: a similar argument exists: '--listen'",
			usage:   "Usage: codex app-server --listen <URL>",
		},
		{
			name:    "app-server proxy --bogus",
			args:    []string{"app-server", "proxy", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server proxy [OPTIONS]",
		},
		{
			name:    "exec-server --remo ws://h:1",
			args:    []string{"exec-server", "--remo", "ws://h:1"},
			message: "unexpected argument '--remo' found\n\n  tip: a similar argument exists: '--remote'",
			usage:   "Usage: codex exec-server --remote <URL>",
		},
		{
			name:    "remote-control start --bogus",
			args:    []string{"remote-control", "start", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex remote-control start [OPTIONS]",
		},
		{
			name:    "migrate-rollouts --thred x",
			args:    []string{"migrate-rollouts", "--thred", "x"},
			message: "unexpected argument '--thred' found\n\n  tip: a similar argument exists: '--thread'",
			usage:   "Usage: codex migrate-rollouts --thread <THREAD_ID>",
		},
		{
			name:    "apply --taks abc",
			args:    []string{"apply", "--taks", "abc"},
			message: "unexpected argument '--taks' found\n\n  tip: to pass '--taks' as a value, use '-- --taks'",
			usage:   "Usage: codex apply [OPTIONS] <TASK_ID>",
		},
		{
			name:    "debug prompt-input --bogus",
			args:    []string{"debug", "prompt-input", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex debug prompt-input [OPTIONS] [PROMPT]",
		},
		{
			name:    "mcp list --bogus",
			args:    []string{"mcp", "list", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex mcp list [OPTIONS]",
		},
		{
			name:    "mcp remove --bogus n",
			args:    []string{"mcp", "remove", "--bogus", "n"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex mcp remove [OPTIONS] <NAME>",
		},
		{
			name:    "mcp login --bogus n",
			args:    []string{"mcp", "login", "--bogus", "n"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex mcp login [OPTIONS] <NAME>",
		},
		{
			name:    "plugin add --bogus src",
			args:    []string{"plugin", "add", "--bogus", "src"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex plugin add [OPTIONS] <PLUGIN[@MARKETPLACE]>",
		},
		{
			name:    "plugin marketplace add --bogus src",
			args:    []string{"plugin", "marketplace", "add", "--bogus", "src"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex plugin marketplace add [OPTIONS] <SOURCE>",
		},
		{
			name:    "plugin marketplace upgrade --bogus",
			args:    []string{"plugin", "marketplace", "upgrade", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex plugin marketplace upgrade [OPTIONS] [MARKETPLACE_NAME]",
		},
		{
			name:    "cloud exec --bogus q",
			args:    []string{"cloud", "exec", "--bogus", "q"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex cloud exec [OPTIONS] --env <ENV_ID> [QUERY]",
		},
		{
			name:    "cloud apply --bogus t",
			args:    []string{"cloud", "apply", "--bogus", "t"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex cloud apply [OPTIONS] <TASK_ID>",
		},
		{
			name:    "cloud diff --bogus t",
			args:    []string{"cloud", "diff", "--bogus", "t"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex cloud diff [OPTIONS] <TASK_ID>",
		},
		{
			name:    "features enable --bogus f",
			args:    []string{"features", "enable", "--bogus", "f"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex features enable [OPTIONS] <FEATURE>",
		},
		{
			name:    "execpolicy check --bogus",
			args:    []string{"execpolicy", "check", "--bogus"},
			message: "the following required arguments were not provided:\n  --rules <PATH>",
			usage:   "Usage: codex execpolicy check --rules <PATH> <COMMAND>...",
		},
		{
			name:    "app-server daemon --bogus",
			args:    []string{"app-server", "daemon", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		},
		{
			name:    "app-server generate-ts --bogus",
			args:    []string{"app-server", "generate-ts", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server generate-ts [OPTIONS] --out <DIR>",
		},
		{
			name:    "debug app-server --bogus",
			args:    []string{"debug", "app-server", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex debug app-server [OPTIONS] <COMMAND>",
		},
		{
			name:    "debug trace-reduce --bogus x",
			args:    []string{"debug", "trace-reduce", "--bogus", "x"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex debug trace-reduce [OPTIONS] <TRACE_BUNDLE>",
		},
		{
			name:    "responses-api-proxy --bogus",
			args:    []string{"responses-api-proxy", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex responses-api-proxy [OPTIONS]",
		},
		{
			name:    "exec-server forward --bogus",
			args:    []string{"exec-server", "forward", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex exec-server forward [OPTIONS] --remote <URL> --environment-id <ID> --connect <URL>",
		},
		{
			name:    "login status --bogus",
			args:    []string{"login", "status", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex login status [OPTIONS]",
		},
		{
			name:    "logout --bogus",
			args:    []string{"logout", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex logout [OPTIONS]",
		},
		{
			name:    "doctor --jso",
			args:    []string{"doctor", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex doctor --json",
		},
		{
			name:    "migrate-nfs-db --bogus",
			args:    []string{"migrate-nfs-db", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex migrate-nfs-db [OPTIONS]",
		},
		{
			name:    "update --bogus",
			args:    []string{"update", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex update [OPTIONS]",
		},
		{
			name:    "agents --bogus",
			args:    []string{"agents", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex agents [OPTIONS]",
		},
		{
			name:    "app --bogus",
			args:    []string{"app", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex app [OPTIONS] [PATH]",
		},
		{
			name:    "app-server generate-ts --outs",
			args:    []string{"app-server", "generate-ts", "--outs"},
			message: "unexpected argument '--outs' found\n\n  tip: a similar argument exists: '--out'",
			usage:   "Usage: codex app-server generate-ts --out <DIR>",
		},
		{
			name:    "app-server generate-ts --experimentl",
			args:    []string{"app-server", "generate-ts", "--experimentl"},
			message: "unexpected argument '--experimentl' found\n\n  tip: a similar argument exists: '--experimental'",
			usage:   "Usage: codex app-server generate-ts --out <DIR> --experimental",
		},
		{
			name:    "app-server generate-ts x",
			args:    []string{"app-server", "generate-ts", "x"},
			message: "unexpected argument 'x' found",
			usage:   "Usage: codex app-server generate-ts [OPTIONS] --out <DIR>",
		},
		{
			name:    "app-server generate-internal-json-schema --bogus",
			args:    []string{"app-server", "generate-internal-json-schema", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server generate-internal-json-schema [OPTIONS] --out <DIR>",
		},
		{
			name:    "app-server generate-internal-json-schema",
			args:    []string{"app-server", "generate-internal-json-schema"},
			message: "the following required arguments were not provided:\n  --out <DIR>",
			usage:   "Usage: codex app-server generate-internal-json-schema --out <DIR>",
		},
		{
			name:    "exec-server forward --remo",
			args:    []string{"exec-server", "forward", "--remo"},
			message: "unexpected argument '--remo' found\n\n  tip: a similar argument exists: '--remote'",
			usage:   "Usage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>",
		},
		{
			name:    "exec-server forward --environment-idd x",
			args:    []string{"exec-server", "forward", "--environment-idd", "x"},
			message: "unexpected argument '--environment-idd' found\n\n  tip: a similar argument exists: '--environment-id'",
			usage:   "Usage: codex exec-server forward --remote <URL> --environment-id <ID> --connect <URL>",
		},
		{
			name:    "exec-server --bogus",
			args:    []string{"exec-server", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex exec-server [OPTIONS] [COMMAND]",
		},
		{
			name:    "exec-server --listn x",
			args:    []string{"exec-server", "--listn", "x"},
			message: "unexpected argument '--listn' found\n\n  tip: a similar argument exists: '--listen'",
			usage:   "Usage: codex exec-server --listen <URL>",
		},
		{
			name:    "login status x",
			args:    []string{"login", "status", "x"},
			message: "unexpected argument 'x' found",
			usage:   "Usage: codex login status [OPTIONS]",
		},
		{
			name:    "login status --with-api-ke",
			args:    []string{"login", "status", "--with-api-ke"},
			message: "unexpected argument '--with-api-ke' found",
			usage:   "Usage: codex login status [OPTIONS]",
		},
		{
			name:    "login --bogus",
			args:    []string{"login", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex login [OPTIONS] [COMMAND]",
		},
		{
			name:    "doctor -x",
			args:    []string{"doctor", "-x"},
			message: "unexpected argument '-x' found",
			usage:   "Usage: codex doctor [OPTIONS]",
		},
		{
			name:    "doctor foo",
			args:    []string{"doctor", "foo"},
			message: "unexpected argument 'foo' found",
			usage:   "Usage: codex doctor [OPTIONS]",
		},
		{
			name:    "doctor --summary --jso",
			args:    []string{"doctor", "--summary", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex doctor --summary --json",
		},
		{
			name:    "plugin add --marketplac x",
			args:    []string{"plugin", "add", "--marketplac", "x"},
			message: "unexpected argument '--marketplac' found\n\n  tip: a similar argument exists: '--marketplace'",
			usage:   "Usage: codex plugin add --marketplace <MARKETPLACE> <PLUGIN[@MARKETPLACE]>",
		},
		{
			name:    "plugin add src --json --bogus",
			args:    []string{"plugin", "add", "src", "--json", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex plugin add --json <PLUGIN[@MARKETPLACE]>",
		},
		{
			name:    "plugin add --json",
			args:    []string{"plugin", "add", "--json"},
			message: "the following required arguments were not provided:\n  <PLUGIN[@MARKETPLACE]>",
			usage:   "Usage: codex plugin add --json <PLUGIN[@MARKETPLACE]>",
		},
		{
			name:    "features enable f --bogus",
			args:    []string{"features", "enable", "f", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex features enable <FEATURE>",
		},
		{
			name:    "features enable --json f",
			args:    []string{"features", "enable", "--json", "f"},
			message: "unexpected argument '--json' found\n\n  tip: to pass '--json' as a value, use '-- --json'",
			usage:   "Usage: codex features enable [OPTIONS] <FEATURE>",
		},
		{
			name:    "mcp list --trnsport",
			args:    []string{"mcp", "list", "--trnsport"},
			message: "unexpected argument '--trnsport' found",
			usage:   "Usage: codex mcp list [OPTIONS]",
		},
		{
			name:    "plugin list --markeplace",
			args:    []string{"plugin", "list", "--markeplace"},
			message: "unexpected argument '--markeplace' found\n\n  tip: a similar argument exists: '--marketplace'",
			usage:   "Usage: codex plugin list --marketplace <MARKETPLACE>",
		},
		{
			name:    "app-server --lsiten",
			args:    []string{"app-server", "--lsiten"},
			message: "unexpected argument '--lsiten' found\n\n  tip: a similar argument exists: '--listen'",
			usage:   "Usage: codex app-server --listen <URL>",
		},
		{
			name:    "debug --bogus",
			args:    []string{"debug", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex debug [OPTIONS] <COMMAND>",
		},
		{
			name:    "features list --enabl",
			args:    []string{"features", "list", "--enabl"},
			message: "unexpected argument '--enabl' found\n\n  tip: a similar argument exists: '--enable'",
			usage:   "Usage: codex features list --enable <FEATURE>",
		},
		{
			name:    "cloud list --limt 3",
			args:    []string{"cloud", "list", "--limt", "3"},
			message: "unexpected argument '--limt' found\n\n  tip: a similar argument exists: '--limit'",
			usage:   "Usage: codex cloud list --limit <N>",
		},
		{
			name:    "cloud exec --en x q",
			args:    []string{"cloud", "exec", "--en", "x", "q"},
			message: "unexpected argument '--en' found\n\n  tip: a similar argument exists: '--env'",
			usage:   "Usage: codex cloud exec --env <ENV_ID> [QUERY]",
		},
		{
			name:    "mcp get --nam x",
			args:    []string{"mcp", "get", "--nam", "x"},
			message: "unexpected argument '--nam' found\n\n  tip: to pass '--nam' as a value, use '-- --nam'",
			usage:   "Usage: codex mcp get [OPTIONS] <NAME>",
		},
		{
			name:    "agents --confg a=b",
			args:    []string{"agents", "--confg", "a=b"},
			message: "unexpected argument '--confg' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex agents --config <key=value>",
		},
		{
			name:    "queue --thred t",
			args:    []string{"queue", "--thred", "t"},
			message: "unexpected argument '--thred' found\n\n  tip: a similar argument exists: '--thread'",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT>",
		},
		{
			name:    "cloud status --task 1",
			args:    []string{"cloud", "status", "--task", "1"},
			message: "unexpected argument '--task' found\n\n  tip: to pass '--task' as a value, use '-- --task'",
			usage:   "Usage: codex cloud status [OPTIONS] <TASK_ID>",
		},
		{
			name:    "debug trace-reduce --bundl x",
			args:    []string{"debug", "trace-reduce", "--bundl", "x"},
			message: "unexpected argument '--bundl' found\n\n  tip: to pass '--bundl' as a value, use '-- --bundl'",
			usage:   "Usage: codex debug trace-reduce [OPTIONS] <TRACE_BUNDLE>",
		},
		{
			name:    "debug clear-memories --al",
			args:    []string{"debug", "clear-memories", "--al"},
			message: "unexpected argument '--al' found",
			usage:   "Usage: codex debug clear-memories [OPTIONS]",
		},
		{
			name:    "debug models --bogus",
			args:    []string{"debug", "models", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex debug models [OPTIONS]",
		},
		{
			name:    "debug prompt-input --jsn",
			args:    []string{"debug", "prompt-input", "--jsn"},
			message: "unexpected argument '--jsn' found\n\n  tip: to pass '--jsn' as a value, use '-- --jsn'",
			usage:   "Usage: codex debug prompt-input [OPTIONS] [PROMPT]",
		},
		{
			name:    "plugin marketplace add --reff x",
			args:    []string{"plugin", "marketplace", "add", "--reff", "x"},
			message: "unexpected argument '--reff' found\n\n  tip: a similar argument exists: '--ref'",
			usage:   "Usage: codex plugin marketplace add --ref <REF> <SOURCE>",
		},
		{
			name:    "plugin marketplace list --jso",
			args:    []string{"plugin", "marketplace", "list", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex plugin marketplace list --json",
		},
		{
			name:    "plugin marketplace upgrade --jso",
			args:    []string{"plugin", "marketplace", "upgrade", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex plugin marketplace upgrade --json [MARKETPLACE_NAME]",
		},
		{
			name:    "plugin marketplace remove --jso",
			args:    []string{"plugin", "marketplace", "remove", "--jso"},
			message: "unexpected argument '--jso' found\n\n  tip: a similar argument exists: '--json'",
			usage:   "Usage: codex plugin marketplace remove --json <MARKETPLACE_NAME>",
		},
		{
			name:    "sandbox uninstall --bogus",
			args:    []string{"sandbox", "uninstall", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex sandbox uninstall [OPTIONS]",
		},
		{
			name:    "sandbox uninstall --al",
			args:    []string{"sandbox", "uninstall", "--al"},
			message: "unexpected argument '--al' found",
			usage:   "Usage: codex sandbox uninstall [OPTIONS]",
		},
		{
			name:    "mcp add --urll u",
			args:    []string{"mcp", "add", "--urll", "u"},
			message: "unexpected argument '--urll' found\n\n  tip: a similar argument exists: '--url'\n  tip: to pass '--urll' as a value, use '-- --urll'",
			usage:   "Usage: codex mcp add [OPTIONS] <NAME> (--url <URL> | -- <COMMAND>...)",
		},
		{
			name:    "app-server daemon --strict-configu",
			args:    []string{"app-server", "daemon", "--strict-configu"},
			message: "unexpected argument '--strict-configu' found",
			usage:   "Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		},
		{
			name:    "apply --task x",
			args:    []string{"apply", "--task", "x"},
			message: "unexpected argument '--task' found\n\n  tip: to pass '--task' as a value, use '-- --task'",
			usage:   "Usage: codex apply [OPTIONS] <TASK_ID>",
		},
		{
			name:    "app-server daemon bootstrap --remot-control x",
			args:    []string{"app-server", "daemon", "bootstrap", "--remot-control", "x"},
			message: "unexpected argument '--remot-control' found\n\n  tip: a similar argument exists: '--remote-control'",
			usage:   "Usage: codex app-server daemon bootstrap --remote-control",
		},
		{
			name:    "app-server daemon start --confi a=b",
			args:    []string{"app-server", "daemon", "start", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon start --config <key=value>",
		},
		{
			name:    "app-server daemon stop --confi a=b",
			args:    []string{"app-server", "daemon", "stop", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon stop --config <key=value>",
		},
		{
			name:    "app-server daemon restart --confi a=b",
			args:    []string{"app-server", "daemon", "restart", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon restart --config <key=value>",
		},
		{
			name:    "app-server daemon version --confi a=b",
			args:    []string{"app-server", "daemon", "version", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon version --config <key=value>",
		},
		{
			name:    "app-server daemon update --fro-cli",
			args:    []string{"app-server", "daemon", "update", "--fro-cli"},
			message: "unexpected argument '--fro-cli' found\n\n  tip: a similar argument exists: '--from-cli'",
			usage:   "Usage: codex app-server daemon update --from-cli",
		},
		{
			name:    "app-server daemon enable-remote-control --confi a=b",
			args:    []string{"app-server", "daemon", "enable-remote-control", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon enable-remote-control --config <key=value>",
		},
		{
			name:    "app-server daemon disable-remote-control --confi a=b",
			args:    []string{"app-server", "daemon", "disable-remote-control", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex app-server daemon disable-remote-control --config <key=value>",
		},
		{
			name:    "debug app-server send-message-v2 --confi a=b",
			args:    []string{"debug", "app-server", "send-message-v2", "--confi", "a=b"},
			message: "unexpected argument '--confi' found\n\n  tip: a similar argument exists: '--config'",
			usage:   "Usage: codex debug app-server send-message-v2 --config <key=value> <USER_MESSAGE>",
		},
		{
			name:    "queue --d x",
			args:    []string{"queue", "--d", "x"},
			message: "unexpected argument '--d' found\n\n  tip: a similar argument exists: '--disable'",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT> --disable <FEATURE>",
		},
		{
			name:    "app-server daemon start foo",
			args:    []string{"app-server", "daemon", "start", "foo"},
			message: "unexpected argument 'foo' found",
			usage:   "Usage: codex app-server daemon start [OPTIONS]",
		},
		{
			name:    "app-server daemon stop --bogus",
			args:    []string{"app-server", "daemon", "stop", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server daemon stop [OPTIONS]",
		},
		{
			name:    "app-server daemon bootstrap --bogus",
			args:    []string{"app-server", "daemon", "bootstrap", "--bogus"},
			message: "unexpected argument '--bogus' found",
			usage:   "Usage: codex app-server daemon bootstrap [OPTIONS]",
		},
		{
			name:    "app-server daemon bootstrap foo",
			args:    []string{"app-server", "daemon", "bootstrap", "foo"},
			message: "unexpected argument 'foo' found",
			usage:   "Usage: codex app-server daemon bootstrap [OPTIONS]",
		},
		{
			name:    "app-server daemon update foo",
			args:    []string{"app-server", "daemon", "update", "foo"},
			message: "unexpected argument 'foo' found",
			usage:   "Usage: codex app-server daemon update [OPTIONS]",
		},
		{
			name:    "app-server daemon update --yes",
			args:    []string{"app-server", "daemon", "update", "--yes"},
			message: "the following required arguments were not provided:\n  --from-cli",
			usage:   "Usage: codex app-server daemon update --from-cli --yes",
		},
		{
			name:    "debug app-server send-message-v2",
			args:    []string{"debug", "app-server", "send-message-v2"},
			message: "the following required arguments were not provided:\n  <USER_MESSAGE>",
			usage:   "Usage: codex debug app-server send-message-v2 <USER_MESSAGE>",
		},
		{
			name:    "debug app-server send-message-v2 a b",
			args:    []string{"debug", "app-server", "send-message-v2", "a", "b"},
			message: "unexpected argument 'b' found",
			usage:   "Usage: codex debug app-server send-message-v2 [OPTIONS] <USER_MESSAGE>",
		},
		{
			name:    "debug app-server send-message-v2 --bogus",
			args:    []string{"debug", "app-server", "send-message-v2", "--bogus"},
			message: "unexpected argument '--bogus' found\n\n  tip: to pass '--bogus' as a value, use '-- --bogus'",
			usage:   "Usage: codex debug app-server send-message-v2 [OPTIONS] <USER_MESSAGE>",
		},
		{
			name:    "plugin marketplace add",
			args:    []string{"plugin", "marketplace", "add"},
			message: "the following required arguments were not provided:\n  <SOURCE>",
			usage:   "Usage: codex plugin marketplace add <SOURCE>",
		},
		{
			name:    "plugin marketplace upgrade x y",
			args:    []string{"plugin", "marketplace", "upgrade", "x", "y"},
			message: "unexpected argument 'y' found",
			usage:   "Usage: codex plugin marketplace upgrade [OPTIONS] [MARKETPLACE_NAME]",
		},
		{
			name:    "plugin marketplace remove",
			args:    []string{"plugin", "marketplace", "remove"},
			message: "the following required arguments were not provided:\n  <MARKETPLACE_NAME>",
			usage:   "Usage: codex plugin marketplace remove <MARKETPLACE_NAME>",
		},
		{
			name:    "app-server daemon version --hel",
			args:    []string{"app-server", "daemon", "version", "--hel"},
			message: "unexpected argument '--hel' found\n\n  tip: a similar argument exists: '--help'",
			usage:   "Usage: codex app-server daemon version --help",
		},
		{
			name:    "queue --cdd x",
			args:    []string{"queue", "--cdd", "x"},
			message: "unexpected argument '--cdd' found\n\n  tip: a similar argument exists: '--cd'",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT> --cd <DIR>",
		},
		{
			name:    "queue --c x",
			args:    []string{"queue", "--c", "x"},
			message: "unexpected argument '--c' found\n\n  tip: a similar argument exists: '--cd'",
			usage:   "Usage: codex queue --thread <THREAD> --message <TEXT> --cd <DIR>",
		},
		{
			name:    "queue --cwd x",
			args:    []string{"queue", "--cwd", "x"},
			message: "unexpected argument '--cwd' found",
			usage:   "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("message = %q, want %q", usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("usage = %q, want %q", usageErr.Usage, testCase.usage)
			}
		})
	}
}
