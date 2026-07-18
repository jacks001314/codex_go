package turn

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDynamicToolsRejectsEmptyNamespace(t *testing.T) {
	err := ValidateDynamicTools([]DynamicToolSpec{{
		Type: "namespace",
		Name: "empty_namespace",
	}})
	if !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("ValidateDynamicTools() error = %v, want ErrInvalidTurnRequest", err)
	}
	if !strings.Contains(err.Error(), "must contain at least one tool") {
		t.Fatalf("ValidateDynamicTools() error = %q", err.Error())
	}
}
