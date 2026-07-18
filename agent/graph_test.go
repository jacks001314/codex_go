package agent

import (
	"encoding/json"
	"testing"
)

func TestThreadSpawnEdgeStatusJSON(t *testing.T) {
	data, err := json.Marshal(&[]ThreadSpawnEdgeStatus{ThreadSpawnEdgeOpen, ThreadSpawnEdgeClosed})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `["open","closed"]` {
		t.Fatalf("json = %s", data)
	}
	var status ThreadSpawnEdgeStatus
	if err := json.Unmarshal([]byte(`"open"`), &status); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if status != ThreadSpawnEdgeOpen {
		t.Fatalf("status = %s", status)
	}
	if err := json.Unmarshal([]byte(`"bad"`), &status); err == nil {
		t.Fatalf("expected invalid status error")
	}
}
