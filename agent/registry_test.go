package agent

import (
	"errors"
	"reflect"
	"testing"
)

func TestRegistryReserveSpawnSlotHonorsLimit(t *testing.T) {
	registry := NewRegistry()
	first, err := registry.ReserveSpawnSlot(1)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot(1) error = %v", err)
	}
	defer first.Cancel()
	if _, err := registry.ReserveSpawnSlot(1); !errors.Is(err, ErrAgentLimitReached) {
		t.Fatalf("ReserveSpawnSlot(over limit) error = %v, want ErrAgentLimitReached", err)
	}
}

func TestRegistryReservationCommitRegistersAgent(t *testing.T) {
	registry := NewRegistry()
	reservation, err := registry.ReserveSpawnSlot(5)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot() error = %v", err)
	}
	nickname, err := reservation.ReserveAgentNickname([]string{"Ada"}, "")
	if err != nil {
		t.Fatalf("ReserveAgentNickname() error = %v", err)
	}
	if nickname != "Ada" {
		t.Fatalf("ReserveAgentNickname() = %q, want Ada", nickname)
	}
	if err := reservation.ReserveAgentPath("/agent/1"); err != nil {
		t.Fatalf("ReserveAgentPath() error = %v", err)
	}
	reservation.Commit(Metadata{
		ThreadID: "thread-1",
		Role:     "explorer",
	})
	got, ok := registry.MetadataForThread("thread-1")
	if !ok {
		t.Fatalf("MetadataForThread() ok = false, want true")
	}
	if got.Path != "/agent/1" || got.Nickname != "Ada" || got.Role != "explorer" {
		t.Fatalf("MetadataForThread() = %#v", got)
	}
}

func TestRegistryReservationCancelReleasesPathNicknameAndCount(t *testing.T) {
	registry := NewRegistry()
	reservation, err := registry.ReserveSpawnSlot(1)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot() error = %v", err)
	}
	if _, err := reservation.ReserveAgentNickname([]string{"Ada"}, "Grace"); err != nil {
		t.Fatalf("ReserveAgentNickname() error = %v", err)
	}
	if err := reservation.ReserveAgentPath("/agent/1"); err != nil {
		t.Fatalf("ReserveAgentPath() error = %v", err)
	}
	reservation.Cancel()
	if registry.TotalSpawned() != 0 {
		t.Fatalf("TotalSpawned() = %d, want 0", registry.TotalSpawned())
	}
	next, err := registry.ReserveSpawnSlot(1)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot(after cancel) error = %v", err)
	}
	defer next.Cancel()
	if _, err := next.ReserveAgentNickname([]string{"Ada"}, "Grace"); err != nil {
		t.Fatalf("ReserveAgentNickname(after cancel) error = %v", err)
	}
	if err := next.ReserveAgentPath("/agent/1"); err != nil {
		t.Fatalf("ReserveAgentPath(after cancel) error = %v", err)
	}
}

func TestRegistryNicknamePoolResetAddsOrdinalSuffix(t *testing.T) {
	registry := NewRegistry()
	first, err := registry.ReserveSpawnSlot(0)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot(first) error = %v", err)
	}
	name, err := first.ReserveAgentNickname([]string{"Ada"}, "")
	if err != nil {
		t.Fatalf("ReserveAgentNickname(first) error = %v", err)
	}
	if name != "Ada" {
		t.Fatalf("first nickname = %q, want Ada", name)
	}
	second, err := registry.ReserveSpawnSlot(0)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot(second) error = %v", err)
	}
	name, err = second.ReserveAgentNickname([]string{"Ada"}, "")
	if err != nil {
		t.Fatalf("ReserveAgentNickname(second) error = %v", err)
	}
	if name != "Ada the 2nd" {
		t.Fatalf("second nickname = %q, want Ada the 2nd", name)
	}
	if registry.NicknameResetCount() != 1 {
		t.Fatalf("NicknameResetCount() = %d, want 1", registry.NicknameResetCount())
	}
}

func TestRegistryLiveAgentsAndRelease(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterRootThread("root")
	registry.RegisterSpawnedThread(Metadata{ThreadID: "thread-2", Path: "/agent/2"})
	registry.RegisterSpawnedThread(Metadata{ThreadID: "thread-1", Path: "/agent/1"})
	agents := registry.LiveAgents()
	got := []string{agents[0].ThreadID, agents[1].ThreadID}
	if !reflect.DeepEqual(got, []string{"thread-1", "thread-2"}) {
		t.Fatalf("LiveAgents() = %v, want thread-1 thread-2", got)
	}
	registry.ReleaseSpawnedThread("thread-1")
	if _, ok := registry.MetadataForThread("thread-1"); ok {
		t.Fatalf("MetadataForThread(thread-1) ok = true after release")
	}
	if id, ok := registry.AgentIDForPath(rootAgentPath); !ok || id != "root" {
		t.Fatalf("AgentIDForPath(root) = %q/%v, want root/true", id, ok)
	}
}

func TestRegistryTaskMessageAndDepthHelpers(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterSpawnedThread(Metadata{ThreadID: "thread-1", Path: "/agent/1"})
	registry.UpdateLastTaskMessage("thread-1", "work")
	metadata, ok := registry.MetadataForThread("thread-1")
	if !ok || metadata.LastTaskMessage != "work" {
		t.Fatalf("MetadataForThread() = %#v/%v", metadata, ok)
	}
	registry.ClearLastTaskMessage("thread-1")
	metadata, _ = registry.MetadataForThread("thread-1")
	if metadata.LastTaskMessage != "" {
		t.Fatalf("ClearLastTaskMessage() = %q, want empty", metadata.LastTaskMessage)
	}
	if NextThreadSpawnDepth("subagent depth=2") != 3 {
		t.Fatalf("NextThreadSpawnDepth() != 3")
	}
	if !ExceedsThreadSpawnDepthLimit(4, 3) {
		t.Fatalf("ExceedsThreadSpawnDepthLimit() = false, want true")
	}
}
