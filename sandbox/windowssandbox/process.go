package windowssandbox

import "sort"

type StdinMode string
type StderrMode string

const (
	StdinModeClosed StdinMode = "closed"
	StdinModeOpen   StdinMode = "open"
	StdinModeNull   StdinMode = StdinModeClosed
	StdinModePipe   StdinMode = StdinModeOpen

	StderrModeMergeStdout StderrMode = "merge_stdout"
	StderrModeSeparate    StderrMode = "separate"
	StderrModePipe        StderrMode = StderrModeSeparate
)

type ProcessStdio struct {
	Stdin  uintptr
	Stdout uintptr
	Stderr uintptr
}

type ProcessSpawnRequest struct {
	Token       uintptr
	Command     []string
	CWD         string
	Env         map[string]string
	LogsBaseDir string
	Stdio       *ProcessStdio
	Desktop     *LaunchDesktop
}

type CreatedProcess struct {
	ProcessHandle uintptr
	ThreadHandle  uintptr
	ProcessID     uint32
	ThreadID      uint32
	StartupFlags  uint32
	Desktop       *LaunchDesktop
}

type PipeSpawnRequest struct {
	Token       uintptr
	Command     []string
	CWD         string
	Env         map[string]string
	StdinMode   StdinMode
	StderrMode  StderrMode
	LogsBaseDir string
	Desktop     *LaunchDesktop
}

type PipeSpawnHandles struct {
	Process       *CreatedProcess
	StdinWrite    uintptr
	StdoutRead    uintptr
	StderrRead    uintptr
	HasStdinWrite bool
	HasStderrRead bool
}

type ProcessWaitOutcome string

const (
	ProcessWaitExited    ProcessWaitOutcome = "exited"
	ProcessWaitTimedOut  ProcessWaitOutcome = "timed_out"
	ProcessWaitCancelled ProcessWaitOutcome = "cancelled"
)

func MakeEnvBlock(env map[string]string) []uint16 {
	keys := orderedEnvEntries(env)
	var out []uint16
	for _, key := range keys {
		item := ToWide(key + "=" + env[key])
		if len(item) > 0 {
			item = item[:len(item)-1]
		}
		out = append(out, item...)
		out = append(out, 0)
	}
	out = append(out, 0)
	return out
}

// orderedEnvEntries mirrors Rust #51512 ordered_env_entries: keys are sorted
// case-insensitively with the original spelling as a tie-break, then duplicate
// Windows spellings collapse so the first entry is the one the child receives.
// Permission resolution and the serialized environment must select the same
// TEMP/TMP value.
func orderedEnvEntries(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		upperI := upperASCII(keys[i])
		upperJ := upperASCII(keys[j])
		if upperI == upperJ {
			return keys[i] < keys[j]
		}
		return upperI < upperJ
	})
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		upper := upperASCII(key)
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		out = append(out, key)
	}
	return out
}

func CreateProcessAsUser(command []string, cwd string, env map[string]string) error {
	token, err := GetCurrentTokenForRestriction()
	if err != nil {
		return err
	}
	defer CloseTokenHandle(token)
	created, err := CreateProcessAsUserWithToken(ProcessSpawnRequest{
		Token:   token,
		Command: command,
		CWD:     cwd,
		Env:     env,
	})
	if err != nil {
		return err
	}
	return created.Close()
}

func SpawnProcessWithPipes(command []string, cwd string, env map[string]string) (*PipeSpawnHandles, error) {
	token, err := GetCurrentTokenForRestriction()
	if err != nil {
		return nil, err
	}
	defer CloseTokenHandle(token)
	return SpawnProcessWithPipesWithToken(PipeSpawnRequest{
		Token:      token,
		Command:    command,
		CWD:        cwd,
		Env:        env,
		StdinMode:  StdinModeClosed,
		StderrMode: StderrModeSeparate,
	})
}

func upperASCII(value string) string {
	bytes := []byte(value)
	for i, b := range bytes {
		if b >= 'a' && b <= 'z' {
			bytes[i] = b - ('a' - 'A')
		}
	}
	return string(bytes)
}
