package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

const daybreakPropagationFeatureOn = "[features]\ndaybreak_program_propagation = true\n"

func newDaybreakPropagationRouter(t *testing.T, home string, store *session.Store) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func writeDaybreakPropagationConfig(t *testing.T, home string, contents string) {
	t.Helper()
	if contents == "" {
		return
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeDaybreakPropagationRequirements(t *testing.T, home string, contents string) {
	t.Helper()
	if contents == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
}

// Rust #62879: a new root thread joins Daybreak propagation when the feature is
// enabled unless the client opts out; managed requirements override the opt-out.
func TestThreadStartDaybreakPropagationEnrollmentLikeRust(t *testing.T) {
	boolPtr := func(value bool) *bool { return &value }
	cases := []struct {
		name         string
		config       string
		requirements string
		field        *bool
		want         bool
	}{
		{"enabled default", daybreakPropagationFeatureOn, "", nil, true},
		{"enabled explicit true", daybreakPropagationFeatureOn, "", boolPtr(true), true},
		{"enabled opt out", daybreakPropagationFeatureOn, "", boolPtr(false), false},
		{"feature disabled", "[features]\ndaybreak_program_propagation = false\n", "", nil, false},
		{"managed requirement wins", daybreakPropagationFeatureOn, daybreakPropagationFeatureOn, boolPtr(false), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeDaybreakPropagationConfig(t, home, tc.config)
			writeDaybreakPropagationRequirements(t, home, tc.requirements)
			router := newDaybreakPropagationRouter(t, home, session.NewStore(filepath.Join(home, "sessions")))
			started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD:                                t.TempDir(),
				DaybreakProgramPropagationEnrolled: tc.field,
			}))
			if started.Error != nil {
				t.Fatalf("thread start error: %+v", started.Error)
			}
			thread := started.Result.(*ThreadStartResponse).Thread
			if thread.DaybreakProgramPropagationEnrolled == nil {
				t.Fatal("thread.daybreakProgramPropagationEnrolled = null, want a live value")
			}
			if got := *thread.DaybreakProgramPropagationEnrolled; got != tc.want {
				t.Fatalf("thread.daybreakProgramPropagationEnrolled = %v, want %v", got, tc.want)
			}
			encoded, err := json.Marshal(thread)
			if err != nil {
				t.Fatalf("marshal thread: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("unmarshal thread: %v", err)
			}
			if decoded["daybreakProgramPropagationEnrolled"] != tc.want {
				t.Fatalf("wire daybreakProgramPropagationEnrolled = %#v, want %v", decoded["daybreakProgramPropagationEnrolled"], tc.want)
			}
		})
	}
}

// Rust #62879: enrollment is live-session state. Stored metadata alone reports
// null, a cold resume reports false, and a live resume keeps the enrollment.
func TestDaybreakPropagationEnrollmentIsSessionLocalLikeRust(t *testing.T) {
	home := t.TempDir()
	writeDaybreakPropagationConfig(t, home, daybreakPropagationFeatureOn)
	store := session.NewStore(filepath.Join(home, "sessions"))
	live := newDaybreakPropagationRouter(t, home, store)
	started := live.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatalf("thread start error: %+v", started.Error)
	}
	thread := started.Result.(*ThreadStartResponse).Thread
	if thread.DaybreakProgramPropagationEnrolled == nil || !*thread.DaybreakProgramPropagationEnrolled {
		t.Fatalf("live start enrollment = %#v, want true", thread.DaybreakProgramPropagationEnrolled)
	}
	threadID := thread.ID
	// Materialize a rollout so the live resume below reaches the history read,
	// matching Rust's start_turn_and_wait_for_completion before its reads.
	record, err := live.services.ThreadRouter.appendThreadItems(session.ThreadID(threadID), []session.Item{{
		ID: "history-marker", Type: "message", Role: "user", Text: "hello", CreatedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("append history item: %v", err)
	}
	if err := live.services.ThreadRouter.createThreadRollout(record, time.Now().UTC()); err != nil {
		t.Fatalf("create rollout: %v", err)
	}

	stored := newDaybreakPropagationRouter(t, home, store)
	read := stored.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: threadID}))
	if read.Error != nil {
		t.Fatalf("thread read error: %+v", read.Error)
	}
	if got := read.Result.(*ThreadReadResponse).Thread.DaybreakProgramPropagationEnrolled; got != nil {
		t.Fatalf("stored thread enrollment = %v, want null", *got)
	}

	liveResume := live.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true}))
	if liveResume.Error != nil {
		t.Fatalf("live resume error: %+v", liveResume.Error)
	}
	if got := liveResume.Result.(*ThreadResumeResponse).Thread.DaybreakProgramPropagationEnrolled; got == nil || !*got {
		t.Fatalf("live resume enrollment = %#v, want the preserved true", got)
	}

	coldResume := stored.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: threadID, ExcludeTurns: true}))
	if coldResume.Error != nil {
		t.Fatalf("cold resume error: %+v", coldResume.Error)
	}
	if got := coldResume.Result.(*ThreadResumeResponse).Thread.DaybreakProgramPropagationEnrolled; got == nil || *got {
		t.Fatalf("cold resume enrollment = %#v, want false", got)
	}
}

// Rust #62879 spawn.rs: a helper propagates Daybreak only when its parent
// thread joined the experiment at startup.
func TestOptedOutRootBlocksHelperDaybreakPropagationLikeRust(t *testing.T) {
	home := t.TempDir()
	writeDaybreakPropagationConfig(t, home, daybreakPropagationFeatureOn)
	router := newDaybreakPropagationRouter(t, home, session.NewStore(filepath.Join(home, "sessions")))
	optOut := false
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:                                t.TempDir(),
		DaybreakProgramPropagationEnrolled: &optOut,
	}))
	if started.Error != nil {
		t.Fatalf("thread start error: %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: t.TempDir()}
	if err := router.threads.RegisterTurn(threadID, "turn-1", nil, 0, params); err != nil {
		t.Fatalf("register turn: %v", err)
	}
	controller := &runtimeAgentController{router: router, parentID: threadID}
	if controller.helperDaybreakPropagationEnabled() {
		t.Fatal("opted-out root propagated Daybreak to a helper")
	}

	enrolled := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if enrolled.Error != nil {
		t.Fatalf("thread start error: %+v", enrolled.Error)
	}
	enrolledID := enrolled.Result.(*ThreadStartResponse).Thread.ID
	enrolledParams := &turn.TurnStartParams{ThreadID: enrolledID, CWD: t.TempDir()}
	if err := router.threads.RegisterTurn(enrolledID, "turn-2", nil, 0, enrolledParams); err != nil {
		t.Fatalf("register turn: %v", err)
	}
	enrolledController := &runtimeAgentController{router: router, parentID: enrolledID}
	if !enrolledController.helperDaybreakPropagationEnabled() {
		t.Fatal("enrolled root did not propagate Daybreak to a helper")
	}
}
