package appserver

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

type probeLogRecord struct {
	message string
	attrs   map[string]string
}

type probeLogHandler struct {
	mu      sync.Mutex
	records []probeLogRecord
}

func (h *probeLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *probeLogHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]string{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.String()
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, probeLogRecord{message: record.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *probeLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *probeLogHandler) WithGroup(string) slog.Handler { return h }

func (h *probeLogHandler) preparationRecord(t *testing.T) probeLogRecord {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		if record.message == "Guardian V3 review preparation started" {
			return record
		}
	}
	t.Fatalf("preparation log missing from %#v", h.records)
	return probeLogRecord{}
}

// Rust #63327 logs the serving probe with the tool call when Guardian V3
// prepares a review. The probe is observational: a high, low or unreported value
// leaves the verdict with the review.
func TestGuardianProbeReviewLoggedAtPreparationLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		assessment *state.ServingAssessment
		want       string
	}{
		{name: "reported_high", assessment: &state.ServingAssessment{RequiresReview: true}, want: "required"},
		{name: "reported_low", assessment: &state.ServingAssessment{}, want: "not_required"},
		{name: "not_reported", assessment: nil, want: "not_reported"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := &probeLogHandler{}
			previous := slog.Default()
			slog.SetDefault(slog.New(handler))
			t.Cleanup(func() { slog.SetDefault(previous) })

			reviewer := &modelGuardianReviewer{
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
				}),
				probeAssessment: func(string, string, string) *state.ServingAssessment {
					return testCase.assessment
				},
			}
			decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1",
				state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
			if err != nil || decision != state.DecisionApproved {
				t.Fatalf("decision=%s err=%v, want approved regardless of the probe", decision, err)
			}
			record := handler.preparationRecord(t)
			if record.attrs["tool_call_id"] != "call-1" {
				t.Fatalf("tool_call_id = %q", record.attrs["tool_call_id"])
			}
			if record.attrs["probe_review"] != testCase.want {
				t.Fatalf("probe_review = %q, want %q", record.attrs["probe_review"], testCase.want)
			}
			if record.attrs["review_id"] == "" {
				t.Fatal("review_id missing from the preparation log")
			}
		})
	}
}
