package state

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const TaskCompactMetricName = "codex.task.compact"

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrTaskAlreadyRunning = errors.New("task already running")
	ErrTaskCancelled      = errors.New("task cancelled")
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type Task struct {
	ID        string
	Kind      TaskKind
	SpanName  string
	CreatedAt time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	Status    TaskStatus
	Error     string
	Result    string
	Cancel    *TaskCancelToken
}

type TaskRunnerFunc func(*TaskContext) (string, error)

type TaskContext struct {
	TaskID string
	Kind   TaskKind
	Cancel *TaskCancelToken
	Values map[string]any
}

func (c *TaskContext) Cancelled() bool {
	return c != nil && c.Cancel != nil && c.Cancel.Cancelled()
}

func (c *TaskContext) CheckCancelled() error {
	if c.Cancelled() {
		return ErrTaskCancelled
	}
	return nil
}

type TaskCancelToken struct {
	mu        sync.Mutex
	cancelled bool
	reason    string
}

func NewTaskCancelToken() *TaskCancelToken {
	return &TaskCancelToken{}
}

func (t *TaskCancelToken) Cancel(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancelled = true
	t.reason = reason
}

func (t *TaskCancelToken) Cancelled() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancelled
}

func (t *TaskCancelToken) Reason() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason
}

type TaskRegistry struct {
	mu      sync.Mutex
	tasks   map[string]*Task
	runners map[TaskKind]TaskRunnerFunc
	now     func() time.Time
}

func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{
		tasks:   map[string]*Task{},
		runners: map[TaskKind]TaskRunnerFunc{},
		now:     time.Now,
	}
}

func (r *TaskRegistry) SetClock(clock func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

func (r *TaskRegistry) Register(kind TaskKind, runner TaskRunnerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if runner == nil {
		delete(r.runners, kind)
		return
	}
	r.runners[kind] = runner
}

func (r *TaskRegistry) Start(id string, kind TaskKind, values map[string]any) (*Task, error) {
	r.mu.Lock()
	if _, exists := r.tasks[id]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrTaskAlreadyRunning, id)
	}
	runner := r.runners[kind]
	now := r.now().UTC()
	started := now
	task := &Task{
		ID:        id,
		Kind:      kind,
		SpanName:  taskSpanName(kind),
		CreatedAt: now,
		StartedAt: &started,
		Status:    TaskStatusRunning,
		Cancel:    NewTaskCancelToken(),
	}
	r.tasks[id] = task
	r.mu.Unlock()

	result := ""
	var err error
	if runner != nil {
		result, err = runner(&TaskContext{TaskID: id, Kind: kind, Cancel: task.Cancel, Values: cloneTaskValues(values)})
	}
	r.finish(id, result, err)
	return r.Get(id)
}

func (r *TaskRegistry) Cancel(id string, reason string) (*Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if task.Cancel != nil {
		task.Cancel.Cancel(reason)
	}
	if task.Status == TaskStatusRunning || task.Status == TaskStatusPending {
		now := r.now().UTC()
		task.Status = TaskStatusCancelled
		task.Error = reason
		task.EndedAt = &now
	}
	return cloneTaskRecord(task), nil
}

func (r *TaskRegistry) Get(id string) (*Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	if task == nil {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return cloneTaskRecord(task), nil
}

func (r *TaskRegistry) List(status TaskStatus) []*Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		if status != "" && task.Status != status {
			continue
		}
		out = append(out, cloneTaskRecord(task))
	}
	sortTaskRecords(out)
	return out
}

func (r *TaskRegistry) finish(id string, result string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	if task == nil {
		return
	}
	if task.Status == TaskStatusCancelled {
		return
	}
	now := r.now().UTC()
	task.EndedAt = &now
	task.Result = result
	if errors.Is(err, ErrTaskCancelled) || task.Cancel.Cancelled() {
		task.Status = TaskStatusCancelled
		task.Error = firstNonEmpty(task.Cancel.Reason(), errString(err))
		return
	}
	if err != nil {
		task.Status = TaskStatusFailed
		task.Error = err.Error()
		return
	}
	task.Status = TaskStatusCompleted
}

type TaskMetric struct {
	Name string
	Inc  int
	Tags map[string]string
	At   time.Time
}

type TaskMetrics struct {
	mu      sync.Mutex
	records []*TaskMetric
	now     func() time.Time
}

func NewTaskMetrics() *TaskMetrics {
	return &TaskMetrics{now: time.Now}
}

func (m *TaskMetrics) SetClock(clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if clock == nil {
		m.now = time.Now
		return
	}
	m.now = clock
}

func (m *TaskMetrics) Counter(name string, inc int, tags map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inc == 0 {
		inc = 1
	}
	m.records = append(m.records, &TaskMetric{
		Name: name,
		Inc:  inc,
		Tags: cloneStringMap(tags),
		At:   m.now().UTC(),
	})
}

func (m *TaskMetrics) Records() []*TaskMetric {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*TaskMetric, 0, len(m.records))
	for _, record := range m.records {
		if record == nil {
			continue
		}
		cloned := *record
		cloned.Tags = cloneStringMap(record.Tags)
		out = append(out, &cloned)
	}
	return out
}

func EmitTaskCompactMetric(metrics *TaskMetrics, compactType string, manual bool) {
	if metrics == nil {
		return
	}
	metrics.Counter(TaskCompactMetricName, 1, map[string]string{
		"type":   compactType,
		"manual": boolTag(manual),
	})
}

func taskSpanName(kind TaskKind) string {
	switch kind {
	case TaskCompact:
		return "session_task.compact"
	case TaskReview:
		return "session_task.review"
	case TaskUserShellCommand:
		return "session_task.user_shell_command"
	default:
		return "session_task.regular"
	}
}

func cloneTaskRecord(task *Task) *Task {
	if task == nil {
		return nil
	}
	cloned := *task
	return &cloned
}

func cloneTaskValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func sortTaskRecords(tasks []*Task) {
	for i := 1; i < len(tasks); i++ {
		current := tasks[i]
		j := i - 1
		for j >= 0 && tasks[j].CreatedAt.After(current.CreatedAt) {
			tasks[j+1] = tasks[j]
			j--
		}
		tasks[j+1] = current
	}
}

func boolTag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
