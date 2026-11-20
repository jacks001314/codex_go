package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/runtimeutil"
)

type sequencedMcpHookExecutor struct {
	outputs []HookMcpOutput
	errs    []error
	calls   int
}

func (e *sequencedMcpHookExecutor) Execute(_ context.Context, _ HookMcpCall) (HookMcpOutput, error) {
	index := e.calls
	e.calls++
	var output HookMcpOutput
	if index < len(e.outputs) {
		output = e.outputs[index]
	}
	var err error
	if index < len(e.errs) {
		err = e.errs[index]
	}
	return output, err
}

func postModelRequestMCPHook(key string, order int64) HookMetadata {
	server := "billing"
	tool := "price"
	return HookMetadata{
		Key:          key,
		EventName:    HookEventPostModelRequest,
		HandlerType:  HookHandlerMCPTool,
		Server:       &server,
		Tool:         &tool,
		TimeoutSec:   5,
		SourcePath:   "/repo/hooks.json",
		Source:       HookSourceUser,
		DisplayOrder: order,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
}

func budgetPrice(units float64) HookMcpOutput {
	return HookMcpOutput{StructuredContent: map[string]any{"extra_codex_rollout_budget_units": units}}
}

func runBillablePostHook(t *testing.T, executor McpToolHookExecutor, hooks ...HookMetadata) *HookRunResult {
	t.Helper()
	runner := NewHookRunner()
	runner.McpToolHookExecutor = executor
	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:             "thread-1",
		CWD:                  t.TempDir(),
		EventName:            HookEventPostModelRequest,
		BillableModelRequest: true,
		Hooks:                hooks,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

// Mirrors Rust #61580: a successful synchronous PostModelRequest MCP hook may
// price its response through structuredContent.
func TestPostModelRequestMCPHookCarriesRolloutBudgetPriceLikeRust(t *testing.T) {
	result := runBillablePostHook(t, &sequencedMcpHookExecutor{outputs: []HookMcpOutput{budgetPrice(3)}}, postModelRequestMCPHook("price", 1))
	if result.ModelRequestHookCharge != (HookCharge{Units: 3}) {
		t.Fatalf("hook charge = %#v, want 3 units", result.ModelRequestHookCharge)
	}
	if result.ModelRequestHookInvalidPrice != "" {
		t.Fatalf("invalid price = %q, want empty", result.ModelRequestHookInvalidPrice)
	}
}

// A non-billable run, a failed sibling, and structured content without the
// price field never add spending.
func TestPostModelRequestMCPHookIgnoresUnbillableRunsLikeRust(t *testing.T) {
	runner := NewHookRunner()
	runner.McpToolHookExecutor = &sequencedMcpHookExecutor{outputs: []HookMcpOutput{budgetPrice(999)}}
	notBillable, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventPostModelRequest,
		Hooks:     []HookMetadata{postModelRequestMCPHook("price", 1)},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if notBillable.ModelRequestHookCharge != (HookCharge{}) {
		t.Fatalf("non-billable charge = %#v, want zero", notBillable.ModelRequestHookCharge)
	}

	failed := runBillablePostHook(t, &sequencedMcpHookExecutor{
		outputs: []HookMcpOutput{budgetPrice(999)},
		errs:    []error{errors.New("MCP tool returned an error: failed")},
	}, postModelRequestMCPHook("failed", 1))
	if failed.ModelRequestHookCharge != (HookCharge{}) || failed.ModelRequestHookInvalidPrice != "" {
		t.Fatalf("failed hook result = %#v, want no charge", failed)
	}

	unpriced := runBillablePostHook(t, &sequencedMcpHookExecutor{
		outputs: []HookMcpOutput{{StructuredContent: map[string]any{}}},
	}, postModelRequestMCPHook("price", 1))
	if unpriced.ModelRequestHookCharge != (HookCharge{}) {
		t.Fatalf("unpriced hook charge = %#v, want zero", unpriced.ModelRequestHookCharge)
	}
}

// An invalid sibling price never erases a valid sibling's contribution, and
// the failure is reported with Rust's wording.
func TestPostModelRequestMCPHookKeepsValidSiblingPriceLikeRust(t *testing.T) {
	result := runBillablePostHook(t, &sequencedMcpHookExecutor{outputs: []HookMcpOutput{
		{StructuredContent: map[string]any{"extra_codex_rollout_budget_units": -1}},
		budgetPrice(2),
	}}, postModelRequestMCPHook("invalid", 1), postModelRequestMCPHook("valid", 2))
	if result.ModelRequestHookCharge != (HookCharge{Units: 2}) {
		t.Fatalf("hook charge = %#v, want the valid sibling's 2 units", result.ModelRequestHookCharge)
	}
	if !strings.Contains(result.ModelRequestHookInvalidPrice, "must be finite and nonnegative") {
		t.Fatalf("invalid price = %q", result.ModelRequestHookInvalidPrice)
	}

	notAnObject := runBillablePostHook(t, &sequencedMcpHookExecutor{
		outputs: []HookMcpOutput{{StructuredContent: "priced"}},
	}, postModelRequestMCPHook("invalid", 1))
	if !strings.Contains(notAnObject.ModelRequestHookInvalidPrice, "must be an object or null") {
		t.Fatalf("non-object price = %q", notAnObject.ModelRequestHookInvalidPrice)
	}
}

// An unrepresentable hook total carries no units and no invalid-price
// diagnostic; the receipt is refused before posting (Rust #62559).
func TestPostModelRequestMCPHookOverflowReportsOverflowLikeRust(t *testing.T) {
	result := runBillablePostHook(t, &sequencedMcpHookExecutor{outputs: []HookMcpOutput{
		budgetPrice(1e308),
		budgetPrice(1e308),
	}}, postModelRequestMCPHook("first", 1), postModelRequestMCPHook("second", 2))
	if result.ModelRequestHookCharge != (HookCharge{Overflow: true}) {
		t.Fatalf("hook charge = %#v, want a bare overflow", result.ModelRequestHookCharge)
	}
	if result.ModelRequestHookInvalidPrice != "" {
		t.Fatalf("invalid price = %q, want no overflow diagnostic", result.ModelRequestHookInvalidPrice)
	}
}

func TestApplyModelRequestHookChargePostsSharedBudgetLikeRust(t *testing.T) {
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitBudget: 1, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	router := NewRuntimeRouter(RuntimeServices{})
	router.rolloutBudget = budget

	if err := router.applyModelRequestHookCharge("thread-1", "turn-1", &HookRunResult{ModelRequestHookCharge: HookCharge{Units: 1}}); err != nil {
		t.Fatalf("applyModelRequestHookCharge() error = %v", err)
	}
	if budget.Exhausted() {
		t.Fatal("spending exactly the allowance closed the work gate")
	}
	exhausted, err := budget.RecordUsage(runtimeutil.TokenUsage{CodexRolloutBudgetUnits: json.Number("0.1")})
	if err != nil || !exhausted {
		t.Fatalf("provider charge after hook: exhausted=%v err=%v", exhausted, err)
	}
}

func TestApplyModelRequestHookChargeSurfacesInvalidPriceLikeRust(t *testing.T) {
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitBudget: 1, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	router := NewRuntimeRouter(RuntimeServices{})
	router.rolloutBudget = budget

	err := router.applyModelRequestHookCharge("thread-1", "turn-1", &HookRunResult{
		ModelRequestHookCharge:       HookCharge{Units: 1},
		ModelRequestHookInvalidPrice: "hook extra_codex_rollout_budget_units must be finite and nonnegative",
	})
	if !errors.Is(err, model.ErrUsageAccountingFailed) {
		t.Fatalf("error = %v, want ErrUsageAccountingFailed", err)
	}
	if !strings.Contains(err.Error(), "Invalid post-hook budget units") {
		t.Fatalf("error = %v, want Rust's accounting message", err)
	}
	// The valid sibling price was posted before the failure surfaced.
	exhausted, usageErr := budget.RecordUsage(runtimeutil.TokenUsage{CodexRolloutBudgetUnits: json.Number("0.1")})
	if usageErr != nil || !exhausted {
		t.Fatalf("valid sibling charge missing: exhausted=%v err=%v", exhausted, usageErr)
	}
}

func TestApplyModelRequestHookChargeRejectsOverflowBeforePostingLikeRust(t *testing.T) {
	budget := runtimeutil.NewBudget()
	budget.Configure(runtimeutil.BudgetConfig{LimitBudget: math.MaxFloat64, PrefillTokenWeight: 1, SamplingTokenWeight: 1})
	router := NewRuntimeRouter(RuntimeServices{})
	router.rolloutBudget = budget

	err := router.applyModelRequestHookCharge("thread-1", "turn-1", &HookRunResult{
		ModelRequestHookCharge: HookCharge{Overflow: true},
	})
	if !errors.Is(err, model.ErrUsageAccountingFailed) || !strings.Contains(err.Error(), "total hook cost exceeds f64 range") {
		t.Fatalf("error = %v, want the overflow accounting failure", err)
	}
	if !budget.Exhausted() {
		t.Fatal("overflow did not close the work gate")
	}
	// The refused receipt never reached the balance.
	exhausted, usageErr := budget.RecordUsage(runtimeutil.TokenUsage{CodexRolloutBudgetUnits: json.Number("1")})
	if usageErr != nil || exhausted {
		t.Fatalf("charge after overflow: exhausted=%v err=%v", exhausted, usageErr)
	}
}

// A host-owned account must not receive a partial charge for an
// unrepresentable hook total (Rust #62559).
func TestApplyModelRequestHookChargeOverflowSkipsHostReportLikeRust(t *testing.T) {
	accounting := &recordingUsageAccounting{}
	router := NewRuntimeRouter(RuntimeServices{UsageAccounting: accounting})

	err := router.applyModelRequestHookCharge("thread-1", "turn-1", &HookRunResult{
		ModelRequestHookCharge: HookCharge{Overflow: true},
	})
	if !errors.Is(err, model.ErrUsageAccountingFailed) || !strings.Contains(err.Error(), "total hook cost exceeds f64 range") {
		t.Fatalf("error = %v, want the overflow accounting failure", err)
	}
	if len(accounting.reports) != 0 {
		t.Fatalf("host reports = %#v, want none", accounting.reports)
	}
}

// recordingUsageAccounting keeps every report a host received.
type recordingUsageAccounting struct {
	reports []runtimeutil.AgentUsageReport
}

func (a *recordingUsageAccounting) ReportUsage(report runtimeutil.AgentUsageReport) (runtimeutil.UsageAccountingDecision, error) {
	a.reports = append(a.reports, report)
	return runtimeutil.UsageAccountingContinue, nil
}
