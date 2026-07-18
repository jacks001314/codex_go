package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
)

func runCloud(ctx context.Context, opts *cli.CloudOptions, stdin io.Reader, stdout io.Writer) error {
	switch opts.Action {
	case "tui":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		return renderCloudTaskBrowser(ctx, stdout, client, opts)
	case "exec":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		query, err := resolveCloudQuery(opts, stdin)
		if err != nil {
			return err
		}
		created, err := client.CreateTask(ctx, &chatgptapi.CloudCreateTaskParams{
			EnvironmentID: opts.Environment,
			Prompt:        query,
			Branch:        resolveCloudBranch(ctx, opts.Branch),
			BestOfN:       opts.Attempts,
		})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, chatgptapi.CloudTaskURL(client.BaseURL(), created.ID))
		return nil
	case "status":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		taskID, err := chatgptapi.ParseCloudTaskID(opts.TaskID)
		if err != nil {
			return err
		}
		summary, err := client.GetTaskSummary(ctx, taskID)
		if err != nil {
			return err
		}
		return renderCloudTaskSummary(stdout, summary)
	case "list":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		page, err := listCloudTasks(ctx, client, opts)
		if err != nil {
			return err
		}
		if opts.JSON {
			return renderCloudTaskListJSON(stdout, page)
		}
		return renderCloudTaskList(stdout, client, page)
	case "diff":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		taskID, err := chatgptapi.ParseCloudTaskID(opts.TaskID)
		if err != nil {
			return err
		}
		diff, err := selectedCloudAttemptDiff(ctx, client, taskID, opts.Attempt)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, diff)
		if err == nil && !strings.HasSuffix(diff, "\n") {
			_, err = io.WriteString(stdout, "\n")
		}
		return err
	case "apply":
		client, err := newCloudTaskClient(opts)
		if err != nil {
			return err
		}
		taskID, err := chatgptapi.ParseCloudTaskID(opts.TaskID)
		if err != nil {
			return err
		}
		diff, err := selectedCloudAttemptDiff(ctx, client, taskID, opts.Attempt)
		if err != nil {
			return err
		}
		if err := runGitApply(ctx, diff, true); err != nil {
			return err
		}
		if err := runGitApply(ctx, diff, false); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Applied task %s\n", taskID)
		return nil
	default:
		return fmt.Errorf("unknown cloud subcommand %s", opts.Action)
	}
}

type cloudAttemptDiff struct {
	Placement *int64
	CreatedAt *time.Time
	Diff      string
}

func selectedCloudAttemptDiff(ctx context.Context, client *chatgptapi.CloudClient, taskID string, attempt int) (string, error) {
	attempts, err := collectCloudAttemptDiffs(ctx, client, taskID)
	if err != nil {
		return "", err
	}
	if len(attempts) == 0 {
		return "", fmt.Errorf("cloud task %s does not have a diff", taskID)
	}
	desired := attempt
	if desired <= 0 {
		desired = 1
	}
	index := desired - 1
	if index >= len(attempts) {
		return "", fmt.Errorf("cloud task %s attempt %d is not available; only %d attempt(s) found", taskID, desired, len(attempts))
	}
	return attempts[index].Diff, nil
}

func collectCloudAttemptDiffs(ctx context.Context, client *chatgptapi.CloudClient, taskID string) ([]*cloudAttemptDiff, error) {
	text, err := client.GetTaskText(ctx, taskID)
	if err != nil {
		return nil, err
	}
	attempts := []*cloudAttemptDiff{}
	if diff, ok, err := client.GetTaskDiff(ctx, taskID); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(diff) != "" {
		attempts = append(attempts, &cloudAttemptDiff{
			Placement: cloneAppInt64Pointer(text.AttemptPlacement),
			Diff:      diff,
		})
	}
	if text.TurnID != nil && strings.TrimSpace(*text.TurnID) != "" {
		siblings, err := client.ListSiblingAttempts(ctx, taskID, *text.TurnID)
		if err != nil {
			return nil, err
		}
		for i := range siblings {
			sibling := &siblings[i]
			if sibling.Diff == nil || strings.TrimSpace(*sibling.Diff) == "" {
				continue
			}
			attempts = append(attempts, &cloudAttemptDiff{
				Placement: cloneAppInt64Pointer(sibling.AttemptPlacement),
				CreatedAt: cloneAppTimePointer(sibling.CreatedAt),
				Diff:      *sibling.Diff,
			})
		}
	}
	sortCloudAttemptDiffs(attempts)
	return attempts, nil
}

func sortCloudAttemptDiffs(attempts []*cloudAttemptDiff) {
	sort.SliceStable(attempts, func(i int, j int) bool {
		left := attempts[i]
		right := attempts[j]
		switch {
		case left.Placement != nil && right.Placement != nil:
			return *left.Placement < *right.Placement
		case left.Placement != nil:
			return true
		case right.Placement != nil:
			return false
		case left.CreatedAt != nil && right.CreatedAt != nil:
			return left.CreatedAt.Before(*right.CreatedAt)
		case left.CreatedAt != nil:
			return true
		case right.CreatedAt != nil:
			return false
		default:
			return false
		}
	})
}

func newCloudTaskClient(opts *cli.CloudOptions) (*chatgptapi.CloudClient, error) {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffective(codexHome, opts.ConfigOverrides, nil, nil)
	if err != nil {
		return nil, err
	}
	baseURL := firstNonEmptyLocal(
		os.Getenv("CODEX_CLOUD_TASKS_BASE_URL"),
		cloudConfigStringValue(loaded.Values, "cloud_tasks_base_url"),
		cloudConfigStringValue(loaded.Values, "cloud_tasks", "base_url"),
		cloudConfigStringValue(loaded.Values, "chatgpt_base_url"),
		chatgptapi.DefaultCloudTasksBaseURL,
	)
	headers := http.Header{}
	resolved, err := auth.NewStore(codexHome).Resolve()
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		authHeaders, err := model.AuthHeadersFromAuth(resolved.Auth)
		if err != nil {
			return nil, err
		}
		mergeHTTPHeadersLocal(headers, authHeaders.Headers)
	}
	return chatgptapi.NewCloudClient(&chatgptapi.CloudClientOptions{
		BaseURL: baseURL,
		Headers: headers,
	}), nil
}

func listCloudTasks(ctx context.Context, client *chatgptapi.CloudClient, opts *cli.CloudOptions) (*chatgptapi.CloudTaskListPage, error) {
	return client.ListTasks(ctx, &chatgptapi.CloudListTasksParams{
		Limit:         opts.Limit,
		TaskFilter:    "current",
		EnvironmentID: opts.Environment,
		Cursor:        opts.Cursor,
	})
}

func renderCloudTaskBrowser(ctx context.Context, stdout io.Writer, client *chatgptapi.CloudClient, opts *cli.CloudOptions) error {
	page, err := listCloudTasks(ctx, client, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return renderCloudTaskListJSON(stdout, page)
	}
	if _, err := fmt.Fprintln(stdout, "Codex Cloud tasks"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	return renderCloudTaskList(stdout, client, page)
}

func renderCloudTaskListJSON(stdout io.Writer, page *chatgptapi.CloudTaskListPage) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(page)
}

func renderCloudTaskList(stdout io.Writer, client *chatgptapi.CloudClient, page *chatgptapi.CloudTaskListPage) error {
	if page == nil || len(page.Tasks) == 0 {
		fmt.Fprintln(stdout, "No tasks found.")
		return nil
	}
	for index := range page.Tasks {
		task := &page.Tasks[index]
		if err := renderCloudTaskListItem(stdout, client, task); err != nil {
			return err
		}
		if index+1 < len(page.Tasks) {
			if _, err := fmt.Fprintln(stdout); err != nil {
				return err
			}
		}
	}
	if page.Cursor != nil && strings.TrimSpace(*page.Cursor) != "" {
		fmt.Fprintf(stdout, "\nTo fetch the next page, run codex cloud list --cursor=%q\n", *page.Cursor)
	}
	return nil
}

func renderCloudTaskListItem(stdout io.Writer, client *chatgptapi.CloudClient, task *chatgptapi.CloudTaskSummary) error {
	if task == nil {
		return nil
	}
	if _, err := fmt.Fprintln(stdout, chatgptapi.CloudTaskURL(client.BaseURL(), task.ID)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "  [%s] %s\n", cloudTaskStatusLabel(task.Status), firstNonEmptyLocal(task.Title, task.ID)); err != nil {
		return err
	}
	meta := cloudTaskMetaParts(task, time.Now().UTC())
	if len(meta) > 0 {
		if _, err := fmt.Fprintf(stdout, "  %s\n", strings.Join(meta, "  -  ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "  %s\n", cloudDiffSummaryLine(&task.Summary))
	return err
}

func renderCloudTaskSummary(stdout io.Writer, summary *chatgptapi.CloudTaskSummary) error {
	if summary == nil {
		return errors.New("cloud task summary is nil")
	}
	for _, line := range cloudTaskStatusLines(summary, time.Now().UTC()) {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func cloudTaskStatusLines(task *chatgptapi.CloudTaskSummary, now time.Time) []string {
	if task == nil {
		return nil
	}
	lines := []string{
		fmt.Sprintf("[%s] %s", cloudTaskStatusLabel(task.Status), firstNonEmptyLocal(task.Title, task.ID)),
	}
	if meta := cloudTaskMetaParts(task, now); len(meta) > 0 {
		lines = append(lines, strings.Join(meta, "  -  "))
	}
	lines = append(lines, cloudDiffSummaryLine(&task.Summary))
	return lines
}

func cloudTaskMetaParts(task *chatgptapi.CloudTaskSummary, now time.Time) []string {
	if task == nil {
		return nil
	}
	parts := []string{}
	if task.EnvironmentLabel != nil && strings.TrimSpace(*task.EnvironmentLabel) != "" {
		parts = append(parts, strings.TrimSpace(*task.EnvironmentLabel))
	} else if task.EnvironmentID != nil && strings.TrimSpace(*task.EnvironmentID) != "" {
		parts = append(parts, strings.TrimSpace(*task.EnvironmentID))
	}
	parts = append(parts, formatCloudRelativeTime(now, task.UpdatedAt))
	if task.AttemptTotal > 1 {
		parts = append(parts, fmt.Sprintf("%d attempts", task.AttemptTotal))
	}
	if task.IsReview {
		parts = append(parts, "review")
	}
	return parts
}

func cloudTaskStatusLabel(status chatgptapi.CloudTaskStatus) string {
	switch status {
	case chatgptapi.CloudTaskStatusReady:
		return "READY"
	case chatgptapi.CloudTaskStatusApplied:
		return "APPLIED"
	case chatgptapi.CloudTaskStatusError:
		return "ERROR"
	default:
		return "PENDING"
	}
}

func cloudDiffSummaryLine(summary *chatgptapi.CloudDiffSummary) string {
	if summary == nil || (summary.FilesChanged == 0 && summary.LinesAdded == 0 && summary.LinesRemoved == 0) {
		return "no diff"
	}
	filesLabel := "files"
	if summary.FilesChanged == 1 {
		filesLabel = "file"
	}
	return fmt.Sprintf("+%d/-%d  -  %d %s", summary.LinesAdded, summary.LinesRemoved, summary.FilesChanged, filesLabel)
}

func formatCloudRelativeTime(now time.Time, value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown time"
	}
	then := value.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	delta := now.Sub(then)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	case delta < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	default:
		return then.Format("2006-01-02")
	}
}

func resolveCloudQuery(opts *cli.CloudOptions, stdin io.Reader) (string, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "-" || query == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		readQuery := strings.TrimSpace(string(data))
		if query == "-" || readQuery != "" {
			query = readQuery
		}
	}
	if query == "" {
		return "", errors.New("cloud exec requires a prompt argument or prompt text on stdin")
	}
	return query, nil
}

func runGitApply(ctx context.Context, diff string, check bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if check {
		args = append(args, "--check")
	}
	command := exec.CommandContext(ctx, "git", args...)
	command.Stdin = strings.NewReader(diff)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), message)
	}
	return nil
}

func resolveCloudBranch(ctx context.Context, branch string) string {
	branch = strings.TrimSpace(branch)
	if branch != "" {
		return branch
	}
	command := exec.CommandContext(ctx, "git", "branch", "--show-current")
	output, err := command.Output()
	if err == nil {
		if current := strings.TrimSpace(string(output)); current != "" {
			return current
		}
	}
	return "main"
}

func cloudConfigStringValue(values map[string]any, path ...string) string {
	var current any = values
	for _, part := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = next[part]
	}
	value, _ := current.(string)
	return strings.TrimSpace(value)
}

func mergeHTTPHeadersLocal(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func cloneAppInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneAppTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
