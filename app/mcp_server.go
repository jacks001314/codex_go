package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
)

type codexMCPRunner struct {
	codexHome string
	root      cli.RootOptions
}

func newCodexMCPRunner(codexHome string, root cli.RootOptions) *codexMCPRunner {
	return &codexMCPRunner{codexHome: codexHome, root: mcpServerRootOptions(&root)}
}

func (r *codexMCPRunner) RunCodexTool(ctx context.Context, params *mcp.CodexToolCall) (*mcp.CodexToolResult, error) {
	if params == nil || strings.TrimSpace(params.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	request, err := codexToolExecRequest(params)
	if err != nil {
		return nil, err
	}
	return r.runExecRequest(ctx, request)
}

func (r *codexMCPRunner) ReplyCodexTool(ctx context.Context, params *mcp.CodexToolReplyCall) (*mcp.CodexToolResult, error) {
	if params == nil || strings.TrimSpace(params.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	threadID := strings.TrimSpace(stringPtrValue(params.ThreadID))
	if threadID == "" {
		threadID = strings.TrimSpace(stringPtrValue(params.ConversationID))
	}
	if threadID == "" {
		return nil, fmt.Errorf("either threadId or conversationId must be provided")
	}
	request := &codexexec.Request{
		Exec: cli.ExecOptions{
			Subcommand: "resume",
			Resume: cli.ExecResumeOptions{
				SessionID: threadID,
				Prompt:    params.Prompt,
			},
		},
	}
	if record, err := session.NewStore(filepath.Join(r.codexHome, "sessions")).Load(session.ThreadID(threadID)); err == nil && record != nil {
		request.Exec.Shared.CWD = strings.TrimSpace(record.Metadata.CWD)
	}
	return r.runExecRequest(ctx, request)
}

func (r *codexMCPRunner) runExecRequest(ctx context.Context, request *codexexec.Request) (*mcp.CodexToolResult, error) {
	r.applyRootToRequest(request)
	warnings, err := r.applySkillsToRequest(request)
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result, err := newCodexExecRunner(r.codexHome).RunContext(ctx, request, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			message = message + ": " + detail
		}
		return nil, fmt.Errorf("%s", message)
	}
	if result == nil {
		return &mcp.CodexToolResult{}, nil
	}
	return &mcp.CodexToolResult{
		ThreadID: result.ThreadID,
		TurnID:   result.TurnID,
		Content:  result.LastMessage,
		Warnings: warnings,
	}, nil
}

func (r *codexMCPRunner) applySkillsToRequest(request *codexexec.Request) ([]string, error) {
	if r == nil || request == nil {
		return nil, nil
	}
	cwd := strings.TrimSpace(request.Exec.Shared.CWD)
	if cwd == "" {
		cwd = strings.TrimSpace(request.Root.Shared.CWD)
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	overrides := append([]string(nil), request.Root.ConfigOverrides...)
	overrides = append(overrides, request.Exec.ConfigOverrides...)
	cfg, err := config.LoadEffective(r.codexHome, overrides, request.Root.EnableFeatures, request.Root.DisableFeatures, cwd)
	if err != nil {
		return nil, err
	}
	modelID := strings.TrimSpace(stringConfigValueApp(cfg.Values, "model"))
	contextWindow := model.ModelInfoFromSlug(modelID).ContextWindow
	skills, err := appserver.BuildHostSkillsPromptContext(&appserver.HostSkillsPromptOptions{
		CodexHome:     r.codexHome,
		CWD:           cwd,
		Config:        cfg,
		Prompt:        mcpExecRequestPrompt(request),
		ContextWindow: contextWindow,
	})
	if err != nil {
		return nil, err
	}
	request.AdditionalInstructions = strings.Join(nonEmptyStringsApp([]string{skills.Instructions, request.AdditionalInstructions}), "\n\n")
	request.AdditionalInputItems = append(request.AdditionalInputItems, skills.InputItems...)
	return append([]string(nil), skills.Warnings...), nil
}

func mcpExecRequestPrompt(request *codexexec.Request) string {
	if request == nil {
		return ""
	}
	if strings.TrimSpace(request.Exec.Prompt) != "" {
		return request.Exec.Prompt
	}
	return request.Exec.Resume.Prompt
}

func stringConfigValueApp(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func (r *codexMCPRunner) rootOptions() cli.RootOptions {
	if r == nil {
		return cli.RootOptions{}
	}
	return mcpServerRootOptions(&r.root)
}

func (r *codexMCPRunner) applyRootToRequest(request *codexexec.Request) {
	if request == nil {
		return
	}
	request.Root = r.rootOptions()
}

func codexToolExecRequest(params *mcp.CodexToolCall) (*codexexec.Request, error) {
	if params == nil {
		return nil, fmt.Errorf("codex tool params are required")
	}
	execOptions := cli.ExecOptions{
		Prompt: strings.TrimSpace(params.Prompt),
	}
	if params.Model != nil {
		execOptions.Shared.Model = strings.TrimSpace(*params.Model)
	}
	if params.CWD != nil {
		execOptions.Shared.CWD = strings.TrimSpace(*params.CWD)
	}
	if params.Sandbox != nil {
		execOptions.Shared.Sandbox = strings.TrimSpace(*params.Sandbox)
	}
	overrides, err := codexToolConfigOverrides(params)
	if err != nil {
		return nil, err
	}
	execOptions.ConfigOverrides = overrides
	return &codexexec.Request{Exec: execOptions}, nil
}

func codexToolConfigOverrides(params *mcp.CodexToolCall) ([]string, error) {
	if params == nil {
		return nil, nil
	}
	values := map[string]any{}
	for key, value := range params.Config {
		values[key] = value
	}
	if params.ApprovalPolicy != nil {
		values["approval_policy"] = *params.ApprovalPolicy
	}
	if params.BaseInstructions != nil {
		values["instructions"] = *params.BaseInstructions
	}
	if params.DeveloperInstructions != nil {
		values["developer_instructions"] = *params.DeveloperInstructions
	}
	if params.CompactPrompt != nil {
		values["compact_prompt"] = *params.CompactPrompt
	}
	return flattenCodexToolOverrides("", values)
}

func flattenCodexToolOverrides(prefix string, values map[string]any) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := values[key].(map[string]any); ok {
			items, err := flattenCodexToolOverrides(path, nested)
			if err != nil {
				return nil, err
			}
			out = append(out, items...)
			continue
		}
		value, ok, err := codexToolOverrideValue(values[key])
		if err != nil {
			return nil, fmt.Errorf("config override %s: %w", path, err)
		}
		if !ok {
			continue
		}
		out = append(out, path+"="+value)
	}
	return out, nil
}

func codexToolOverrideValue(value any) (string, bool, error) {
	switch typed := value.(type) {
	case nil:
		return "", false, nil
	case string:
		return strconv.Quote(typed), true, nil
	case bool:
		return strconv.FormatBool(typed), true, nil
	case int:
		return strconv.Itoa(typed), true, nil
	case int64:
		return strconv.FormatInt(typed, 10), true, nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true, nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok, err := codexToolOverrideValue(item)
			if err != nil {
				return "", false, err
			}
			if ok {
				items = append(items, value)
			}
		}
		return "[" + strings.Join(items, ", ") + "]", true, nil
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, strconv.Quote(item))
		}
		return "[" + strings.Join(items, ", ") + "]", true, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	}
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var _ mcp.CodexToolRunner = (*codexMCPRunner)(nil)
