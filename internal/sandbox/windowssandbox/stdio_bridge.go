package windowssandbox

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

const (
	StdinForwardChunkSize = 8 * 1024
	OutputDrainTimeout    = 5 * time.Second
)

type SandboxSessionIO interface {
	SendStdin([]byte) error
	CloseStdin()
	RequestTerminate()
}

type SpawnedSandboxSession struct {
	Session SandboxSessionIO
	Stdout  <-chan []byte
	Stderr  <-chan []byte
	Exit    <-chan int
}

func ForwardSandboxSessionStdio(spawned SpawnedSandboxSession, stdin io.Reader, stdout io.Writer, stderr io.Writer, interrupt <-chan struct{}) int {
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	stdinDone := SpawnInputForwarder(stdin, spawned.Session)
	stdoutDone := SpawnOutputForwarder(spawned.Stdout, stdout)
	stderrDone := SpawnOutputForwarder(spawned.Stderr, stderr)

	exitCode := -1
	select {
	case code, ok := <-spawned.Exit:
		if ok {
			exitCode = code
		}
	case <-interrupt:
		if spawned.Session != nil {
			spawned.Session.RequestTerminate()
		}
		code, ok := <-spawned.Exit
		if ok {
			exitCode = code
		}
	}

	waitForOutputDrain(stdinDone, stdoutDone, stderrDone)
	return exitCode
}

func SpawnInputForwarder(input io.Reader, session SandboxSessionIO) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if session != nil {
				session.CloseStdin()
			}
		}()
		if input == nil {
			return
		}
		buffer := make([]byte, StdinForwardChunkSize)
		for {
			n, err := input.Read(buffer)
			if n > 0 && session != nil {
				chunk := append([]byte(nil), buffer[:n]...)
				if sendErr := session.SendStdin(chunk); sendErr != nil {
					return
				}
			}
			if err == nil {
				continue
			}
			if err == io.EOF {
				return
			}
			if isInterruptedRead(err) {
				continue
			}
			_, _ = fmt.Fprintf(os.Stderr, "windows sandbox stdin forwarder failed: %v\n", err)
			return
		}
	}()
	return done
}

func SpawnOutputForwarder(output <-chan []byte, writer io.Writer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if writer == nil {
			writer = io.Discard
		}
		for chunk := range output {
			if _, err := writer.Write(chunk); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "windows sandbox output forwarder failed to write: %v\n", err)
				return
			}
			if flusher, ok := writer.(interface{ Flush() error }); ok {
				if err := flusher.Flush(); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "windows sandbox output forwarder failed to flush: %v\n", err)
					return
				}
			}
		}
	}()
	return done
}

func waitForOutputDrain(doneChans ...<-chan struct{}) {
	deadline := time.After(OutputDrainTimeout)
	for _, done := range doneChans {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-deadline:
			return
		}
	}
}

func isInterruptedRead(err error) bool {
	if err == nil {
		return false
	}
	return err == syscall.EINTR
}
