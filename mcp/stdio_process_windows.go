//go:build windows

package mcp

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mcpJobObjectLimitKillOnJobClose mirrors Rust's
// `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` for the local MCP server job.
const mcpJobObjectLimitKillOnJobClose = 0x2000

var procMCPNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// mcpStdioProcess contains one local stdio MCP server in a Windows Job Object so
// a server that spawns helpers is torn down as a tree.
type mcpStdioProcess struct {
	mu         sync.Mutex
	job        windows.Handle
	process    windows.Handle
	jobHeld    bool
	terminated bool
}

// startMCPStdioProcess spawns the server suspended, assigns it to a job object,
// and resumes it, mirroring Rust's `JobObject::create_without_breakaway` +
// `prepare_suspended_spawn` + `assign_and_resume_process`. An unavailable job
// object degrades to an uncontained child, which Rust also does (with a
// warning) instead of failing the server.
func startMCPStdioProcess(cmd *exec.Cmd) (*mcpStdioProcess, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return startUncontainedMCPStdioProcess(cmd)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: mcpJobObjectLimitKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return startUncontainedMCPStdioProcess(cmd)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	// The child must not run before it can be assigned to the job, or a helper it
	// spawns could escape containment.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		killMCPStdioDirectChild(cmd)
		_ = cmd.Wait()
		return nil, fmt.Errorf("open MCP server process: %w", err)
	}
	assignmentErr := windows.AssignProcessToJobObject(job, process)
	if resumeErr := resumeSuspendedMCPProcess(process); resumeErr != nil {
		windows.CloseHandle(process)
		windows.CloseHandle(job)
		killMCPStdioDirectChild(cmd)
		_ = cmd.Wait()
		return nil, resumeErr
	}
	if assignmentErr != nil {
		// Nested jobs can reject assignment. Keep the resumed child and fall back
		// to terminating the direct process (Rust's `Ok(false)` arm).
		windows.CloseHandle(job)
		return &mcpStdioProcess{process: process}, nil
	}
	return &mcpStdioProcess{job: job, process: process, jobHeld: true}, nil
}

func startUncontainedMCPStdioProcess(cmd *exec.Cmd) (*mcpStdioProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &mcpStdioProcess{}, nil
}

func resumeSuspendedMCPProcess(handle windows.Handle) error {
	status, _, callErr := procMCPNtResumeProcess.Call(uintptr(handle))
	if status != 0 {
		if callErr != nil {
			return fmt.Errorf("resume suspended MCP server process: %v", callErr)
		}
		return fmt.Errorf("resume suspended MCP server process: NTSTATUS %#x", uint32(status))
	}
	return nil
}

// terminate kills every process assigned to the job, or the retained direct
// process when job assignment was unavailable. It is idempotent.
func (p *mcpStdioProcess) terminate(cmd *exec.Cmd) {
	if p == nil {
		killMCPStdioDirectChild(cmd)
		return
	}
	p.mu.Lock()
	if p.terminated {
		p.mu.Unlock()
		return
	}
	p.terminated = true
	job, process := p.job, p.process
	p.mu.Unlock()
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		return
	}
	if process != 0 {
		_ = windows.TerminateProcess(process, 1)
		return
	}
	killMCPStdioDirectChild(cmd)
}

// release closes the retained handles once the child has been reaped. Closing
// the job handle also terminates surviving members (kill-on-job-close).
func (p *mcpStdioProcess) release() {
	if p == nil {
		return
	}
	p.mu.Lock()
	job, process := p.job, p.process
	p.job, p.process = 0, 0
	p.mu.Unlock()
	if job != 0 {
		windows.CloseHandle(job)
	}
	if process != 0 {
		windows.CloseHandle(process)
	}
}

// contained reports whether the server was assigned to a job object.
func (p *mcpStdioProcess) contained() bool {
	return p != nil && p.jobHeld
}
