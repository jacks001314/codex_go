package tui

type ConfigUpdate struct {
	KeyPath string
	Value   any
}

func ConfigUpdatePath(key string, value any) ConfigUpdate {
	return ConfigUpdate{KeyPath: key, Value: value}
}
