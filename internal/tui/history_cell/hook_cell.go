package historycell

func NewHookCell(eventName string, status string, statusMessage string, entries []HookOutputEntry) HookRunCell {
	return NewHookRun(eventName, status, statusMessage, entries)
}
