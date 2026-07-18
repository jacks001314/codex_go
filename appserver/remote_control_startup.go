package appserver

import "os"

const RemoteControlDisabledEnvVar = "CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED"

func TakeRemoteControlDisabledEnv() bool {
	disabled := os.Getenv(RemoteControlDisabledEnvVar) == "1"
	_ = os.Unsetenv(RemoteControlDisabledEnvVar)
	return disabled
}
