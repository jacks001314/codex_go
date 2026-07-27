package chatgptapi

import (
	"encoding/json"
	"testing"
)

func TestTaskResponseJSONShape(t *testing.T) {
	pr := NewExternalPullRequestResponse("pr-link", "turn-1", NewGitPullRequest(7, "https://example/pr/7", "open", false, true))
	task := NewTaskResponse("task-1", "Fix bug", false, []ExternalPullRequestResponse{*pr})
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["external_pull_requests"] == nil || decoded["id"] != "task-1" {
		t.Fatalf("unexpected json: %s", data)
	}
}

func TestRateLimitPayloadDefaultsGuest(t *testing.T) {
	payload := NewRateLimitStatusPayload("")
	if payload.PlanType != PlanGuest {
		t.Fatalf("plan = %s", payload.PlanType)
	}
	payload.RateLimit = NewRateLimitStatusDetails(true, false)
	if !payload.RateLimit.Allowed || payload.RateLimit.LimitReached {
		t.Fatalf("unexpected rate limit: %#v", payload.RateLimit)
	}
}

func TestRateLimitPayloadPreservesEnt26Plan(t *testing.T) {
	var payload RateLimitStatusPayload
	if err := json.Unmarshal([]byte(`{"plan_type":"ent26"}`), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload.PlanType != PlanEnt26 {
		t.Fatalf("plan = %q, want ent26", payload.PlanType)
	}
	if snapshots := RateLimitSnapshotsFromPayload(&payload); len(snapshots) != 1 || snapshots[0].PlanType != PlanEnt26 {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestConstructorsInitializeRequiredFields(t *testing.T) {
	title := true
	item := NewTaskListItem("task", "Title", &title, true, false)
	if item.ID != "task" || item.HasGeneratedTitle == nil || !item.Archived {
		t.Fatalf("unexpected item: %#v", item)
	}
	credits := NewCreditStatusDetails(true, true)
	if !credits.HasCredits || !credits.Unlimited {
		t.Fatalf("unexpected credits: %#v", credits)
	}
}

func TestRateLimitSnapshotsFromPayload(t *testing.T) {
	balance := "9.99"
	payload := &RateLimitStatusPayload{
		PlanType: PlanPro,
		RateLimit: &RateLimitStatusDetails{
			PrimaryWindow: &RateLimitWindowSnapshot{
				UsedPercent:        42.5,
				LimitWindowSeconds: 3600,
				ResetsAt:           123,
			},
			SecondaryWindow: &RateLimitWindowSnapshot{
				UsedPercent:        84,
				LimitWindowSeconds: 86400,
				ResetsAt:           456,
			},
		},
		Credits: &CreditStatusDetails{HasCredits: true, Unlimited: false, Balance: &balance},
		SpendControl: &SpendControlStatusDetails{IndividualLimit: &SpendControlLimitDetails{
			Limit:            "25000",
			Used:             "8000",
			RemainingPercent: 68,
			ResetsAt:         789,
		}},
		RateLimitReachedType: &RateLimitReachedType{Type: WorkspaceMemberCreditsDepleted},
		AdditionalRateLimits: []AdditionalRateLimitDetails{{
			LimitName:      "Other",
			MeteredFeature: "codex_other",
			RateLimit: &RateLimitStatusDetails{PrimaryWindow: &RateLimitWindowSnapshot{
				UsedPercent:        70,
				LimitWindowSeconds: 900,
				ResetsAt:           987,
			}},
		}},
	}

	snapshots := RateLimitSnapshotsFromPayload(payload)
	if len(snapshots) != 2 {
		t.Fatalf("snapshots len = %d", len(snapshots))
	}
	if snapshots[0].LimitID == nil || *snapshots[0].LimitID != "codex" {
		t.Fatalf("primary snapshot = %#v", snapshots[0])
	}
	if snapshots[0].Primary == nil || snapshots[0].Primary.UsedPercent != 42.5 || snapshots[0].Primary.WindowDurationMins == nil || *snapshots[0].Primary.WindowDurationMins != 60 {
		t.Fatalf("primary window = %#v", snapshots[0].Primary)
	}
	if snapshots[0].Credits == nil || snapshots[0].Credits.Balance == nil || *snapshots[0].Credits.Balance != "9.99" {
		t.Fatalf("credits = %#v", snapshots[0].Credits)
	}
	if snapshots[0].IndividualLimit == nil || snapshots[0].IndividualLimit.RemainingPercent != 68 {
		t.Fatalf("individual limit = %#v", snapshots[0].IndividualLimit)
	}
	if snapshots[0].RateLimitReachedType == nil || *snapshots[0].RateLimitReachedType != WorkspaceMemberCreditsDepleted {
		t.Fatalf("reached type = %#v", snapshots[0].RateLimitReachedType)
	}
	if snapshots[1].LimitID == nil || *snapshots[1].LimitID != "codex_other" || snapshots[1].Primary == nil || snapshots[1].Primary.WindowDurationMins == nil || *snapshots[1].Primary.WindowDurationMins != 15 {
		t.Fatalf("additional snapshot = %#v", snapshots[1])
	}
}
