package agent

import (
	"encoding/json"
	"fmt"
)

type ThreadSpawnEdgeStatus string

const (
	ThreadSpawnEdgeOpen   ThreadSpawnEdgeStatus = "open"
	ThreadSpawnEdgeClosed ThreadSpawnEdgeStatus = "closed"
)

func (s ThreadSpawnEdgeStatus) IsValid() bool {
	return s == ThreadSpawnEdgeOpen || s == ThreadSpawnEdgeClosed
}

func (s ThreadSpawnEdgeStatus) MarshalJSON() ([]byte, error) {
	if !s.IsValid() {
		return nil, fmt.Errorf("invalid thread spawn edge status %q", s)
	}
	return json.Marshal(string(s))
}

func (s *ThreadSpawnEdgeStatus) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	status := ThreadSpawnEdgeStatus(value)
	if !status.IsValid() {
		return fmt.Errorf("invalid thread spawn edge status %q", value)
	}
	*s = status
	return nil
}
