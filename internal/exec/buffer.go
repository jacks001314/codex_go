package exec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	DefaultOutputMaxBytes = 64 * 1024
	MaxProcesses          = 64
)

var (
	ErrUnknownProcessID = errors.New("unknown process id")
	ErrProcessLimit     = errors.New("unified exec process limit reached")
	ErrStdinClosed      = errors.New("stdin is closed for this session; rerun exec_command with tty=true to keep stdin open")
	ErrMissingCommand   = errors.New("missing command line for unified exec request")
)

type HeadTailBuffer struct {
	maxBytes     int
	headBudget   int
	tailBudget   int
	head         [][]byte
	tail         [][]byte
	headBytes    int
	tailBytes    int
	omittedBytes int
}

func NewHeadTailBuffer(maxBytes int) *HeadTailBuffer {
	if maxBytes < 0 {
		maxBytes = 0
	}
	headBudget := maxBytes / 2
	return &HeadTailBuffer{
		maxBytes:   maxBytes,
		headBudget: headBudget,
		tailBudget: maxBytes - headBudget,
	}
}

func (b *HeadTailBuffer) PushChunk(chunk []byte) {
	if b == nil || len(chunk) == 0 {
		return
	}
	copied := append([]byte(nil), chunk...)
	if b.maxBytes == 0 {
		b.omittedBytes += len(copied)
		return
	}
	if b.headBytes < b.headBudget {
		remaining := b.headBudget - b.headBytes
		if len(copied) <= remaining {
			b.head = append(b.head, copied)
			b.headBytes += len(copied)
			return
		}
		if remaining > 0 {
			b.head = append(b.head, copied[:remaining])
			b.headBytes += remaining
		}
		b.pushTail(copied[remaining:])
		return
	}
	b.pushTail(copied)
}

func (b *HeadTailBuffer) SnapshotChunks() [][]byte {
	if b == nil {
		return nil
	}
	out := make([][]byte, 0, len(b.head)+len(b.tail))
	for _, chunk := range b.head {
		out = append(out, append([]byte(nil), chunk...))
	}
	for _, chunk := range b.tail {
		out = append(out, append([]byte(nil), chunk...))
	}
	return out
}

func (b *HeadTailBuffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, 0, b.RetainedBytes())
	for _, chunk := range b.head {
		out = append(out, chunk...)
	}
	for _, chunk := range b.tail {
		out = append(out, chunk...)
	}
	return out
}

func (b *HeadTailBuffer) DrainChunks() [][]byte {
	chunks := b.SnapshotChunks()
	b.head = nil
	b.tail = nil
	b.headBytes = 0
	b.tailBytes = 0
	b.omittedBytes = 0
	return chunks
}

func (b *HeadTailBuffer) RetainedBytes() int {
	if b == nil {
		return 0
	}
	return b.headBytes + b.tailBytes
}

func (b *HeadTailBuffer) OmittedBytes() int {
	if b == nil {
		return 0
	}
	return b.omittedBytes
}

func (b *HeadTailBuffer) pushTail(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	b.reserveOmissionByte()
	if b.tailBudget == 0 {
		b.omittedBytes += len(chunk)
		return
	}
	if len(chunk) >= b.tailBudget {
		start := len(chunk) - b.tailBudget
		b.omittedBytes += b.tailBytes + start
		b.tail = [][]byte{append([]byte(nil), chunk[start:]...)}
		b.tailBytes = b.tailBudget
		return
	}
	b.tail = append(b.tail, chunk)
	b.tailBytes += len(chunk)
	b.trimTail()
}

func (b *HeadTailBuffer) trimTail() {
	excess := b.tailBytes - b.tailBudget
	for excess > 0 && len(b.tail) > 0 {
		front := b.tail[0]
		if excess >= len(front) {
			b.tail = b.tail[1:]
			b.tailBytes -= len(front)
			b.omittedBytes += len(front)
			excess -= len(front)
			continue
		}
		b.tail[0] = append([]byte(nil), front[excess:]...)
		b.tailBytes -= excess
		b.omittedBytes += excess
		break
	}
}

func (b *HeadTailBuffer) reserveOmissionByte() {
	if b == nil || b.omittedBytes > 0 || b.maxBytes <= 1 {
		return
	}
	targetTailBudget := b.maxBytes - b.headBudget - 1
	if targetTailBudget < 0 {
		targetTailBudget = 0
	}
	if b.tailBudget > targetTailBudget {
		b.tailBudget = targetTailBudget
		b.trimTail()
	}
}

type ProcessState struct {
	HasExited      bool
	ExitCode       *int
	FailureMessage string
	SandboxDenied  bool
}

func (s *ProcessState) Exited(exitCode *int) ProcessState {
	next := *s
	next.HasExited = true
	next.ExitCode = exitCode
	return next
}

func (s *ProcessState) Failed(message string) ProcessState {
	next := *s
	next.HasExited = true
	next.FailureMessage = message
	return next
}

type ProcessEntry struct {
	ID      int
	Command []string
	CWD     string
	TTY     bool
	State   ProcessState
	Output  *HeadTailBuffer
	Stdin   []string
	Closed  bool
	CallID  string
}

type Store struct {
	mu        sync.Mutex
	nextID    int
	max       int
	processes map[int]*ProcessEntry
}

func NewStore(maxProcesses int) *Store {
	if maxProcesses <= 0 {
		maxProcesses = MaxProcesses
	}
	return &Store{
		nextID:    1,
		max:       maxProcesses,
		processes: make(map[int]*ProcessEntry),
	}
}

func (s *Store) Create(command []string, cwd string, tty bool) (*ProcessEntry, error) {
	if len(command) == 0 {
		return nil, ErrMissingCommand
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := 0
	for _, entry := range s.processes {
		if !entry.State.HasExited {
			active++
		}
	}
	if active >= s.max {
		return nil, fmt.Errorf("%w: max=%d", ErrProcessLimit, s.max)
	}
	id := s.nextID
	s.nextID++
	entry := &ProcessEntry{
		ID:      id,
		Command: append([]string(nil), command...),
		CWD:     cwd,
		TTY:     tty,
		Output:  NewHeadTailBuffer(DefaultOutputMaxBytes),
		CallID:  fmt.Sprintf("exec-%d", id),
	}
	s.processes[id] = entry
	return cloneEntry(entry), nil
}

func (s *Store) Get(id int) (*ProcessEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.processes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	return cloneEntry(entry), nil
}

func (s *Store) AppendOutput(id int, chunk []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.processes[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	entry.Output.PushChunk(chunk)
	return nil
}

func (s *Store) WriteStdin(id int, input string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.processes[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	if !entry.TTY || entry.Closed || entry.State.HasExited {
		return ErrStdinClosed
	}
	entry.Stdin = append(entry.Stdin, input)
	return nil
}

func (s *Store) MarkExited(id int, exitCode int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.processes[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	entry.State = entry.State.Exited(&exitCode)
	entry.Closed = true
	return nil
}

func (s *Store) MarkFailed(id int, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.processes[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	entry.State = entry.State.Failed(message)
	entry.Closed = true
	return nil
}

func (s *Store) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.processes[id]; !ok {
		return fmt.Errorf("%w: %d", ErrUnknownProcessID, id)
	}
	delete(s.processes, id)
	return nil
}

func (s *Store) List() []ProcessEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int, 0, len(s.processes))
	for id := range s.processes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]ProcessEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, *cloneEntry(s.processes[id]))
	}
	return out
}

func cloneEntry(entry *ProcessEntry) *ProcessEntry {
	if entry == nil {
		return nil
	}
	cloned := *entry
	cloned.Command = append([]string(nil), entry.Command...)
	cloned.Stdin = append([]string(nil), entry.Stdin...)
	cloned.Output = NewHeadTailBuffer(entry.Output.maxBytes)
	for _, chunk := range entry.Output.SnapshotChunks() {
		cloned.Output.PushChunk(chunk)
	}
	cloned.Output.omittedBytes = entry.Output.omittedBytes
	return &cloned
}
