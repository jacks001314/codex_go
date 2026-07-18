package conpty

type Instance struct {
	pseudoConsole uintptr
	inputRead     uintptr
	inputWrite    uintptr
	outputRead    uintptr
	outputWrite   uintptr
}

type SpawnRequest struct {
	Token             uintptr
	Command           []string
	CWD               string
	Env               map[string]string
	UsePrivateDesktop bool
	LogsBaseDir       string
	Columns           int16
	Rows              int16
}

func (i *Instance) RawHandle() uintptr {
	if i == nil {
		return 0
	}
	return i.pseudoConsole
}

func (i *Instance) TakeInputWrite() uintptr {
	if i == nil {
		return 0
	}
	handle := i.inputWrite
	i.inputWrite = 0
	return handle
}

func (i *Instance) TakeOutputRead() uintptr {
	if i == nil {
		return 0
	}
	handle := i.outputRead
	i.outputRead = 0
	return handle
}

func (i *Instance) forgetInputWrite() uintptr {
	if i == nil {
		return 0
	}
	handle := i.inputWrite
	i.inputWrite = 0
	return handle
}
