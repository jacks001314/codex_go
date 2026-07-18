package appserver

import (
	"os"
	osexec "os/exec"
	"testing"
)

func TestRuntimeRouterThreadShellCommandUpdatesTerminalOSPID(t *testing.T) {
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadExtras: extras})
	canceled := false
	extras.AddBackgroundTerminalWithCancel("thread-1", &BackgroundTerminal{
		ItemID:    "item-1",
		ProcessID: "proc-1",
		Command:   "sleep 10",
		CWD:       "/repo",
	}, func() { canceled = true })

	router.updateThreadShellCommandTerminalOSPID(&threadShellCommandRun{ThreadID: "thread-1"}, "proc-1", &osexec.Cmd{
		Process: &os.Process{Pid: 2468},
	})

	list, err := extras.ListBackgroundTerminals(&BackgroundTerminalsListParams{ThreadID: "thread-1"})
	if err != nil || len(list.Data) != 1 || list.Data[0].OSPID == nil || *list.Data[0].OSPID != 2468 {
		t.Fatalf("background terminals after os pid update = %#v err=%v", list, err)
	}
	terminated, err := extras.TerminateBackgroundTerminal(&BackgroundTerminalsTerminateParams{ThreadID: "thread-1", ProcessID: "proc-1"})
	if err != nil || !terminated.Terminated || !canceled {
		t.Fatalf("terminate after os pid update = %#v canceled=%v err=%v", terminated, canceled, err)
	}
}
