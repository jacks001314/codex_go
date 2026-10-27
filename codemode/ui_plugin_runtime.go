package codemode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errUIPluginDeadline = errors.New("plugin execution deadline exceeded")
	errUIPluginClosed   = errors.New("plugin instance closed")
	errUIPluginOutput   = errors.New("plugin instance output full")
)

const uiPluginTurnDeadlineMillis = 100

// RunUIPluginSandbox serves the bounded UI plugin ABI on stdin/stdout. It is
// selected by --codex-ui-plugin-process in codex-code-mode-host (#57508).
func RunUIPluginSandbox(stdin io.Reader, stdout io.Writer) error {
	outputs := make(chan uiPluginQueuedOutput, uiPluginOutputQueueCapacity)
	commands := make(chan uiPluginCommand, uiPluginCommandQueueCapacity)
	fatal := make(chan error, 1)
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	executorDone := make(chan struct{})

	go runUIPluginWriter(stdout, outputs, fatal, writerDone)
	go runUIPluginExecutor(commands, outputs, fatal, executorDone)

	packets := make(chan uiPluginInboundPacket)
	readErrors := make(chan error, 1)
	go func() {
		defer close(packets)
		for {
			payload, ok, err := readUIPluginFrame(stdin)
			if err != nil {
				readErrors <- err
				return
			}
			if !ok {
				return
			}
			packet, err := decodeUIPluginInboundPacket(payload)
			if err != nil {
				readErrors <- err
				return
			}
			select {
			case packets <- packet:
			case <-stop:
				return
			}
		}
	}()

	live := map[uint64]*uiPluginInstance{}
	var lastID uint64
	haveLastID := false
	rejectedSlot := make(chan struct{}, 1)
	for {
		select {
		case err := <-fatal:
			close(stop)
			return err
		case err := <-readErrors:
			close(stop)
			return err
		case packet, ok := <-packets:
			if !ok {
				close(commands)
				<-executorDone
				close(outputs)
				<-writerDone
				return nil
			}
			for id, instance := range live {
				if instance.retired.Load() {
					delete(live, id)
				}
			}
			message, err := decodeUIPluginHostMessage(packet.Message)
			if err != nil {
				close(stop)
				return err
			}
			var instance *uiPluginInstance
			switch message.Type {
			case "initialize":
				if haveLastID && packet.Instance <= lastID {
					close(stop)
					return fmt.Errorf("invalid plugin instance ID")
				}
				lastID, haveLastID = packet.Instance, true
				if len(live) >= uiPluginMaxInstances {
					// Backpressure excess opens instead of accumulating unbounded
					// close frames.
					select {
					case rejectedSlot <- struct{}{}:
					case <-stop:
						return nil
					}
					if err := sendUIPluginOutput(outputs, uiPluginQueuedOutput{
						packets: []uiPluginOutboundPacket{{
							Instance: packet.Instance,
							Message:  uiPluginClosedMessage(),
						}},
						release: func() { <-rejectedSlot },
					}); err != nil {
						close(stop)
						return err
					}
					continue
				}
				instance = newUIPluginInstance(packet.Instance)
				live[instance.id] = instance
			case "dispatch", "close":
				instance = live[packet.Instance]
				if instance == nil {
					continue
				}
			}
			if instance.closed.Load() {
				continue
			}
			var release func()
			if message.Type != "close" {
				select {
				case instance.inputSlots <- struct{}{}:
					release = func() { <-instance.inputSlots }
				default:
					if instance.closed.CompareAndSwap(false, true) {
						// Cancellation bypasses queued JS turns.
						cancel := instance.cancel.Load()
						if cancel != nil {
							(*cancel)(instance.id)
						}
						if err := sendUIPluginCommand(commands, uiPluginCommand{
							kind:     uiPluginCommandClose,
							instance: instance,
						}); err != nil {
							close(stop)
							return err
						}
					}
					continue
				}
			}
			kind := uiPluginCommandTurn
			if message.Type == "close" {
				kind = uiPluginCommandClose
			}
			if err := sendUIPluginCommand(commands, uiPluginCommand{
				kind:     kind,
				message:  message,
				instance: instance,
				release:  release,
			}); err != nil {
				if release != nil {
					release()
				}
				close(stop)
				return err
			}
		}
	}
}

type uiPluginInstance struct {
	id          uint64
	retired     atomic.Bool
	closed      atomic.Bool
	finished    atomic.Bool
	inputSlots  chan struct{}
	outputSlots chan struct{}
	// cancel is installed by the executor so overload can interrupt the active
	// JS turn from the input loop.
	cancel atomic.Pointer[func(uint64)]
}

func newUIPluginInstance(id uint64) *uiPluginInstance {
	return &uiPluginInstance{
		id:          id,
		inputSlots:  make(chan struct{}, uiPluginMaxInFlight),
		outputSlots: make(chan struct{}, uiPluginMaxInFlight+1),
	}
}

const (
	uiPluginCommandTurn = iota
	uiPluginCommandClose
)

type uiPluginCommand struct {
	kind     int
	message  uiPluginHostMessage
	instance *uiPluginInstance
	release  func()
}

type uiPluginQueuedOutput struct {
	packets []uiPluginOutboundPacket
	retired *atomic.Bool
	release func()
}

func sendUIPluginCommand(commands chan<- uiPluginCommand, command uiPluginCommand) error {
	select {
	case commands <- command:
		return nil
	default:
		return errors.New("plugin command queue is full")
	}
}

func sendUIPluginOutput(outputs chan<- uiPluginQueuedOutput, output uiPluginQueuedOutput) error {
	select {
	case outputs <- output:
		return nil
	default:
		return errors.New("plugin output queue is full")
	}
}

func runUIPluginWriter(stdout io.Writer, outputs <-chan uiPluginQueuedOutput, fatal chan<- error, done chan<- struct{}) {
	defer close(done)
	if err := writeUIPluginFrame(stdout, []byte(uiPluginReady)); err != nil {
		fatal <- err
		return
	}
	for output := range outputs {
		if output.retired != nil {
			output.retired.Store(true)
		}
		for _, packet := range output.packets {
			payload, err := json.Marshal(packet)
			if err == nil {
				err = writeUIPluginFrame(stdout, payload)
			}
			if err != nil {
				if output.release != nil {
					output.release()
				}
				fatal <- err
				return
			}
		}
		if output.release != nil {
			output.release()
		}
	}
}

func runUIPluginExecutor(commands <-chan uiPluginCommand, outputs chan<- uiPluginQueuedOutput, fatal chan<- error, done chan<- struct{}) {
	defer close(done)
	watchdog := &uiPluginWatchdog{}
	engines := map[uint64]*uiPluginEngine{}
	for command := range commands {
		instance := command.instance
		if command.kind == uiPluginCommandClose {
			if _, ok := engines[instance.id]; ok {
				delete(engines, instance.id)
				if err := sendUIPluginClose(outputs, instance, nil); err != nil {
					fatal <- err
					return
				}
			}
			continue
		}
		if instance.closed.Load() {
			_, hasEngine := engines[instance.id]
			if hasEngine {
				delete(engines, instance.id)
			}
			if hasEngine || command.message.Type == "initialize" {
				if err := sendUIPluginClose(outputs, instance, nil); err != nil {
					fatal <- err
					return
				}
			}
			if command.release != nil {
				command.release()
			}
			continue
		}
		if command.message.Type == "initialize" {
			engine, err := newUIPluginEngine()
			if err != nil {
				fmt.Fprintf(os.Stderr, "plugin isolate initialization failed: %v\n", err)
				instance.closed.Store(true)
				if closeErr := sendUIPluginClose(outputs, instance, nil); closeErr != nil {
					fatal <- closeErr
					return
				}
				if command.release != nil {
					command.release()
				}
				continue
			}
			engines[instance.id] = engine
		}
		engine := engines[instance.id]
		if engine == nil {
			var failure *uiPluginRuntimeError
			if instance.closed.CompareAndSwap(false, true) {
				failure = &uiPluginRuntimeError{Code: uiPluginErrExecutionFailed}
			}
			if err := sendUIPluginClose(outputs, instance, failure); err != nil {
				fatal <- err
				return
			}
			if command.release != nil {
				command.release()
			}
			continue
		}
		interrupt := func(value any) { engine.runtime.Interrupt(value) }
		cancel := func(id uint64) { watchdog.cancel(id, interrupt) }
		instance.cancel.Store(&cancel)
		watchdog.arm(instance.id, interrupt)
		messages, failure := engine.turn(command.message)
		disarmErr := watchdog.disarm()
		if command.release != nil {
			command.release()
		}
		if disarmErr != nil {
			messages, failure = nil, &uiPluginRuntimeError{Code: uiPluginErrExecutionTimeout}
		}
		if failure == nil {
			if err := writeUIPluginTurn(outputs, instance, messages); err == nil {
				continue
			}
		}
		if instance.closed.Load() {
			failure = nil
		}
		delete(engines, instance.id)
		instance.closed.Store(true)
		if err := sendUIPluginClose(outputs, instance, failure); err != nil {
			fatal <- err
			return
		}
	}
}

func writeUIPluginTurn(outputs chan<- uiPluginQueuedOutput, instance *uiPluginInstance, messages []any) error {
	if instance.closed.Load() {
		return errUIPluginClosed
	}
	if len(messages) == 0 {
		return nil
	}
	select {
	case instance.outputSlots <- struct{}{}:
	default:
		return errUIPluginOutput
	}
	release := func() { <-instance.outputSlots }
	packets := make([]uiPluginOutboundPacket, 0, len(messages))
	for _, message := range messages {
		packets = append(packets, uiPluginOutboundPacket{Instance: instance.id, Message: message})
	}
	if err := sendUIPluginOutput(outputs, uiPluginQueuedOutput{
		packets: packets,
		release: release,
	}); err != nil {
		release()
		return err
	}
	return nil
}

func sendUIPluginClose(outputs chan<- uiPluginQueuedOutput, instance *uiPluginInstance, failure *uiPluginRuntimeError) error {
	if !instance.finished.CompareAndSwap(false, true) {
		return nil
	}
	packets := make([]uiPluginOutboundPacket, 0, 2)
	if failure != nil {
		packets = append(packets, uiPluginOutboundPacket{
			Instance: instance.id,
			Message:  uiPluginErrorMessage(*failure),
		})
	}
	packets = append(packets, uiPluginOutboundPacket{
		Instance: instance.id,
		Message:  uiPluginClosedMessage(),
	})
	return sendUIPluginOutput(outputs, uiPluginQueuedOutput{
		packets: packets,
		retired: &instance.retired,
	})
}

type uiPluginActiveExecution struct {
	instance   uint64
	interrupt  func(any)
	timer      *time.Timer
	terminated atomic.Bool
}

// uiPluginWatchdog bounds one JS turn. A fired timer interrupts the active
// isolate; disarming after termination reports execution_timed_out.
type uiPluginWatchdog struct {
	mu     sync.Mutex
	active *uiPluginActiveExecution
}

func (w *uiPluginWatchdog) arm(instance uint64, interrupt func(any)) {
	active := &uiPluginActiveExecution{instance: instance, interrupt: interrupt}
	active.timer = time.AfterFunc(uiPluginTurnDeadlineMillis*time.Millisecond, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.active == active && !active.terminated.Load() {
			active.terminated.Store(true)
			active.interrupt(errUIPluginDeadline)
		}
	})
	w.mu.Lock()
	w.active = active
	w.mu.Unlock()
}

func (w *uiPluginWatchdog) disarm() error {
	w.mu.Lock()
	active := w.active
	w.active = nil
	w.mu.Unlock()
	if active != nil {
		active.timer.Stop()
		if active.terminated.Load() {
			return errUIPluginDeadline
		}
	}
	return nil
}

func (w *uiPluginWatchdog) cancel(instance uint64, interrupt func(any)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if active := w.active; active != nil && active.instance == instance && !active.terminated.Load() {
		active.terminated.Store(true)
		interrupt(errUIPluginDeadline)
	}
}
