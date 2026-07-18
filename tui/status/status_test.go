package status

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/sandbox"
)

func TestAccountStatusDisplayMatchesRustStatusAccountDisplay(t *testing.T) {
	tests := []struct {
		name string
		in   AccountStatus
		want string
		ok   bool
	}{
		{name: "chatgpt email plan", in: ChatGPTAccountStatus("me@example.com", "Pro"), want: "me@example.com (Pro)", ok: true},
		{name: "chatgpt email only", in: ChatGPTAccountStatus("me@example.com", ""), want: "me@example.com", ok: true},
		{name: "chatgpt plan only", in: ChatGPTAccountStatus("", "Enterprise"), want: "Enterprise", ok: true},
		{name: "chatgpt generic", in: ChatGPTAccountStatus("", ""), want: "ChatGPT", ok: true},
		{name: "api key", in: APIKeyAccountStatus(), want: "API key configured (run codex login to use ChatGPT)", ok: true},
		{name: "missing", in: AccountStatus{}, want: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := tt.in.DisplayValue()
		if got != tt.want || ok != tt.ok {
			t.Fatalf("%s DisplayValue() = %q ok=%v, want %q ok=%v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRemoteConnectionStatusValueMatchesRust(t *testing.T) {
	version := "1.2.3"
	if got := RemoteConnectionStatusValue(RemoteConnectionEmbedded, "", &version); got != nil {
		t.Fatalf("embedded status = %#v, want nil", got)
	}

	got := RemoteConnectionStatusValue(RemoteConnectionWebSocket, "ws://user:secret@127.0.0.1:4500/?token=abc#frag", &version)
	if got == nil || got.Address != "ws://127.0.0.1:4500/" || got.Version != "v1.2.3" {
		t.Fatalf("websocket status = %#v", got)
	}

	socket := RemoteConnectionStatusValue(RemoteConnectionUnixSocket, "/tmp/codex.sock", nil)
	if socket == nil || socket.Address != "unix:///tmp/codex.sock" || socket.Version != "unknown" {
		t.Fatalf("unix status = %#v", socket)
	}

	invalid := RemoteConnectionStatusValue(RemoteConnectionWebSocket, "not a url", nil)
	if invalid == nil || invalid.Address != "<invalid websocket URL>" {
		t.Fatalf("invalid websocket status = %#v", invalid)
	}
}

func TestStatusPermissionsLabelMatchesRustStatusSnapshots(t *testing.T) {
	approval := StatusApprovalLabel(sandbox.ApprovalOnRequest, config.ApprovalsReviewerUser, "on-request")
	if approval != "Ask for approval" {
		t.Fatalf("user approval label = %q", approval)
	}
	autoApproval := StatusApprovalLabel(sandbox.ApprovalOnRequest, config.ApprovalsReviewerAutoReview, "on-request")
	if autoApproval != "Approve for me" {
		t.Fatalf("auto-review approval label = %q", autoApproval)
	}
	if got := StatusApprovalLabel(sandbox.ApprovalNever, config.ApprovalsReviewerAutoReview, "never"); got != "never" {
		t.Fatalf("non on-request approval label = %q", got)
	}

	workspace := &sandbox.ActivePermissionProfile{ID: sandbox.BuiltInPermissionProfileWorkspace}
	readOnly := &sandbox.ActivePermissionProfile{ID: sandbox.BuiltInPermissionProfileReadOnly}
	fullAccess := &sandbox.ActivePermissionProfile{ID: sandbox.BuiltInPermissionProfileDangerFullAccess}
	disabled := &sandbox.PermissionProfile{Disabled: true}

	tests := []struct {
		name     string
		active   *sandbox.ActivePermissionProfile
		profile  *sandbox.PermissionProfile
		policy   sandbox.AskForApproval
		sandbox  string
		approval string
		suffix   string
		want     string
	}{
		{
			name:     "custom workspace network",
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "workspace with network access",
			approval: approval,
			want:     "Custom (workspace with network access, Ask for approval)",
		},
		{
			name:     "named read only",
			active:   readOnly,
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "read-only",
			approval: approval,
			want:     "Read Only (Ask for approval)",
		},
		{
			name:     "named workspace",
			active:   workspace,
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "workspace",
			approval: approval,
			want:     "Workspace (Ask for approval)",
		},
		{
			name:     "workspace auto review",
			active:   workspace,
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "workspace",
			approval: autoApproval,
			want:     "Workspace (Approve for me)",
		},
		{
			name:     "workspace roots",
			active:   &sandbox.ActivePermissionProfile{ID: ":workspace"},
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "workspace",
			approval: approval,
			suffix:   " [/workspace/extra]",
			want:     "Workspace [/workspace/extra] (Ask for approval)",
		},
		{
			name:     "broadened workspace",
			active:   workspace,
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "workspace with network access",
			approval: approval,
			want:     "Workspace with network access (Ask for approval)",
		},
		{
			name:     "user defined profile",
			active:   &sandbox.ActivePermissionProfile{ID: "locked"},
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "read-only",
			approval: approval,
			want:     "Profile locked (read-only, Ask for approval)",
		},
		{
			name:     "full access never",
			active:   fullAccess,
			profile:  disabled,
			policy:   sandbox.ApprovalNever,
			sandbox:  "danger-full-access",
			approval: "never",
			want:     "Full Access",
		},
		{
			name:     "full access on request",
			active:   fullAccess,
			profile:  disabled,
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "danger-full-access",
			approval: approval,
			want:     "No Sandbox (Ask for approval)",
		},
		{
			name:     "managed unrestricted with network",
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "danger-full-access",
			approval: approval,
			want:     "Custom (danger-full-access, Ask for approval)",
		},
		{
			name:     "managed unrestricted without network",
			policy:   sandbox.ApprovalOnRequest,
			sandbox:  "external-sandbox",
			approval: approval,
			want:     "Custom (external-sandbox, Ask for approval)",
		},
	}
	for _, tt := range tests {
		got := StatusPermissionsLabel(tt.active, tt.profile, tt.policy, tt.sandbox, tt.approval, tt.suffix)
		if got != tt.want {
			t.Fatalf("%s StatusPermissionsLabel() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFieldFormatterMatchesRustSpacing(t *testing.T) {
	formatter := NewFieldFormatter([]string{"Model", "Directory", "Context window"})
	if formatter.ValueOffset != 19 {
		t.Fatalf("ValueOffset = %d, want 19", formatter.ValueOffset)
	}
	if got := formatter.Line("Model", "gpt-5"); got != " Model:            gpt-5" {
		t.Fatalf("model line = %q", got)
	}
	if got := formatter.Line("Context window", "42% left"); got != " Context window:   42% left" {
		t.Fatalf("context line = %q", got)
	}
	if got := formatter.Continuation("(resets 12:00)"); got != "                   (resets 12:00)" {
		t.Fatalf("continuation = %q", got)
	}
	if got := formatter.ValueWidth(80); got != 61 {
		t.Fatalf("ValueWidth = %d, want 61", got)
	}
}

func TestHelpersMatchRustFormatting(t *testing.T) {
	if model, details := ComposeModelDisplay("gpt-5.1-codex", []ModelDisplayEntry{
		{Key: "reasoning effort", Value: "HIGH"},
		{Key: "reasoning summaries", Value: "Detailed"},
	}); model != "gpt-5.1-codex" || !reflect.DeepEqual(details, []string{"reasoning high", "summaries detailed"}) {
		t.Fatalf("model display = %q %#v", model, details)
	}
	if _, details := ComposeModelDisplay("gpt", []ModelDisplayEntry{{Key: "reasoning summaries", Value: "off"}}); !reflect.DeepEqual(details, []string{"summaries off"}) {
		t.Fatalf("summary off details = %#v", details)
	}

	tokenCases := map[int64]string{
		-5:                "0",
		0:                 "0",
		999:               "999",
		1_000:             "1K",
		1_234:             "1.23K",
		12_340:            "12.3K",
		123_400:           "123K",
		835_000_000:       "835M",
		21_400_000_000:    "21.4B",
		1_000_000_000_000: "1T",
	}
	for input, want := range tokenCases {
		if got := FormatTokensCompact(input); got != want {
			t.Fatalf("FormatTokensCompact(%d) = %q, want %q", input, got, want)
		}
	}

	planCases := map[auth.PlanType]string{
		auth.PlanFree:                        "Free",
		auth.PlanGo:                          "Go",
		auth.PlanPlus:                        "Plus",
		auth.PlanPro:                         "Pro",
		auth.PlanProlite:                     "Pro Lite",
		auth.PlanTeam:                        "Business",
		auth.PlanSelfServeBusinessUsageBased: "Business",
		auth.PlanBusiness:                    "Enterprise",
		auth.PlanEnterpriseCBPUsageBased:     "Enterprise",
		auth.PlanEnterprise:                  "Enterprise",
		auth.PlanEdu:                         "Edu",
		auth.PlanUnknown:                     "Unknown",
	}
	for input, want := range planCases {
		if got := PlanTypeDisplayName(input); got != want {
			t.Fatalf("PlanTypeDisplayName(%q) = %q, want %q", input, got, want)
		}
	}

	capturedAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.Local)
	if got := FormatResetTimestamp(capturedAt.Add(10*time.Minute), capturedAt); got != "03:14" {
		t.Fatalf("same-day reset = %q", got)
	}
	if got := FormatResetTimestamp(capturedAt.Add(25*time.Hour), capturedAt); got != "04:04 on 3 Jan" {
		t.Fatalf("next-day reset = %q", got)
	}
}

func TestRateLimitRowsMatchRustStatusComposition(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.Local)
	reset := "soon"
	fiveHours := int64(300)
	weekly := int64(10_080)
	balance := "37.5"
	codex := RateLimitSnapshotDisplay{
		LimitName:  "codex",
		CapturedAt: now,
		Primary:    &RateLimitWindowDisplay{UsedPercent: 10, ResetsAt: &reset, WindowMinutes: &fiveHours},
		Secondary:  &RateLimitWindowDisplay{UsedPercent: 40, WindowMinutes: &weekly},
		Credits:    &CreditsSnapshotDisplay{HasCredits: true, Balance: &balance},
	}
	other := RateLimitSnapshotDisplay{
		LimitName:  "codex-other",
		CapturedAt: now,
		Primary:    &RateLimitWindowDisplay{UsedPercent: 20, WindowMinutes: &fiveHours},
	}

	data := ComposeRateLimitDataMany([]RateLimitSnapshotDisplay{codex, other}, now)
	if data.Kind != StatusRateLimitDataAvailable {
		t.Fatalf("data kind = %q", data.Kind)
	}
	labels := make([]string, len(data.Rows))
	for i, row := range data.Rows {
		labels[i] = row.Label
	}
	wantLabels := []string{"5h limit", "Weekly limit", "Credits", "codex-other 5h limit"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("labels = %#v, want %#v", labels, wantLabels)
	}
	if data.Rows[2].Value.Text != "38 credits" {
		t.Fatalf("credit row = %#v", data.Rows[2])
	}
	if data.Rows[0].Value.Kind != StatusRateLimitValueWindow || data.Rows[0].Value.ResetsAt == nil || *data.Rows[0].Value.ResetsAt != "soon" {
		t.Fatalf("window row = %#v", data.Rows[0])
	}

	stale := ComposeRateLimitData(&codex, now.Add(16*time.Minute))
	if stale.Kind != StatusRateLimitDataStale {
		t.Fatalf("stale kind = %q", stale.Kind)
	}
	if missing := ComposeRateLimitData(nil, now); missing.Kind != StatusRateLimitDataMissing {
		t.Fatalf("missing kind = %q", missing.Kind)
	}
	if unavailable := ComposeRateLimitData(&RateLimitSnapshotDisplay{CapturedAt: now}, now); unavailable.Kind != StatusRateLimitDataUnavailable {
		t.Fatalf("unavailable kind = %q", unavailable.Kind)
	}
}

func TestRateLimitRenderingAndCreditFormatting(t *testing.T) {
	if got := RenderStatusLimitProgressBar(50); got != "["+strings.Repeat("\u25a0", 10)+strings.Repeat("\u25a1", 10)+"]" {
		t.Fatalf("progress bar = %q", got)
	}
	if got := FormatStatusLimitSummary(42.4); got != "42% left" {
		t.Fatalf("summary = %q", got)
	}
	if got, ok := FormatCreditBalance("0.4"); !ok || got != "0" {
		t.Fatalf("credit balance = %q ok=%v", got, ok)
	}
	if _, ok := FormatCreditBalance("0"); ok {
		t.Fatalf("zero integer credit balance should be hidden")
	}
	if got, ok := FormatCreditAmount("12345.6"); !ok || got != "12,346" {
		t.Fatalf("credit amount = %q ok=%v", got, ok)
	}
	for _, input := range []string{"NaN", "+Inf", "-Inf", "-1"} {
		if got, ok := FormatCreditAmount(input); ok {
			t.Fatalf("FormatCreditAmount(%q) = %q ok=true, want hidden like Rust", input, got)
		}
	}
}
