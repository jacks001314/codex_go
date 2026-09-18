package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/session"
)

func boolPtrDaybreak(value bool) *bool { return &value }

// TestRuntimeRouterThreadStartCarriesInitialDaybreakChoice mirrors Rust #45513:
// `thread/start.daybreakEnabled` is staged with the initial thread metadata, is
// reported in the start response, and is readable before persistence.
func TestRuntimeRouterThreadStartCarriesInitialDaybreakChoice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   *bool
		wantSet bool
		want    bool
	}{
		{name: "true", value: boolPtrDaybreak(true), wantSet: true, want: true},
		{name: "false", value: boolPtrDaybreak(false), wantSet: true, want: false},
		{name: "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

			start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD:             filepath.ToSlash(t.TempDir()),
				Prompt:          "hello",
				DaybreakEnabled: tc.value,
			}))
			if start.Error != nil {
				t.Fatalf("thread/start error: %+v", start.Error)
			}
			response := start.Result.(*ThreadStartResponse)
			if response.Thread == nil {
				t.Fatal("thread/start response thread is nil")
			}
			got := response.Thread.DaybreakEnabled
			if tc.wantSet {
				if got == nil || *got != tc.want {
					t.Fatalf("thread.daybreakEnabled = %#v, want %v", got, tc.want)
				}
			} else if got != nil {
				t.Fatalf("thread.daybreakEnabled = %v, want unset", *got)
			}

			// Reads observe the staged choice before persistence.
			read := router.Handle(requestWithParams(t, IntID(2), MethodThreadRead, ThreadReadParams{
				ThreadID: response.Thread.ID,
			}))
			if read.Error != nil {
				t.Fatalf("thread/read error: %+v", read.Error)
			}
			readThread := read.Result.(*ThreadReadResponse).Thread
			if tc.wantSet {
				if readThread.DaybreakEnabled == nil || *readThread.DaybreakEnabled != tc.want {
					t.Fatalf("read daybreakEnabled = %#v, want %v", readThread.DaybreakEnabled, tc.want)
				}
			} else if readThread.DaybreakEnabled != nil {
				t.Fatalf("read daybreakEnabled = %v, want unset", *readThread.DaybreakEnabled)
			}

			// The choice is part of the persisted metadata, so a cold read of
			// the same store still reports it.
			record, err := session.NewStore(store.Root()).Load(session.ThreadID(response.Thread.ID))
			if err != nil {
				t.Fatalf("reload thread: %v", err)
			}
			if tc.wantSet {
				if record.Metadata.DaybreakEnabled == nil || *record.Metadata.DaybreakEnabled != tc.want {
					t.Fatalf("persisted daybreakEnabled = %#v, want %v", record.Metadata.DaybreakEnabled, tc.want)
				}
			} else if record.Metadata.DaybreakEnabled != nil {
				t.Fatalf("persisted daybreakEnabled = %v, want unset", *record.Metadata.DaybreakEnabled)
			}
		})
	}
}

// TestRuntimeRouterThreadStartRejectsDaybreakForEphemeralThreads mirrors Rust
// #45513: an ephemeral thread cannot save the initial preference.
func TestRuntimeRouterThreadStartRejectsDaybreakForEphemeralThreads(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(session.NewStore(t.TempDir()))})
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:             filepath.ToSlash(t.TempDir()),
		Prompt:          "hello",
		Ephemeral:       true,
		DaybreakEnabled: boolPtrDaybreak(true),
	}))
	if response.Error == nil || response.Error.Message != "daybreakEnabled is not supported for ephemeral threads" {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
}
