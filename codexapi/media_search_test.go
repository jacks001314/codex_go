package codexapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImageRequestJSON(t *testing.T) {
	background := ImageBackgroundTransparent
	quality := ImageQualityHigh
	data, err := json.Marshal(ImageGenerationRequest{
		Prompt:     "draw",
		Model:      "gpt-image-1",
		Background: &background,
		Quality:    &quality,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"background":"transparent"`) || !strings.Contains(string(data), `"quality":"high"`) {
		t.Fatalf("json = %s", data)
	}
}

func TestSearchCommandsJSON(t *testing.T) {
	length := SearchMedium
	data, err := json.Marshal(SearchCommands{
		SearchQuery:    []SearchQuery{{Q: "codex", Domains: []string{"openai.com"}}},
		Finance:        []FinanceOperation{{Ticker: "AMD", Type: FinanceEquity}},
		ResponseLength: &length,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"search_query"`) || !strings.Contains(string(data), `"response_length":"medium"`) {
		t.Fatalf("json = %s", data)
	}
}

func TestExternalWebAccessUnmarshal(t *testing.T) {
	var access ExternalWebAccess
	if err := json.Unmarshal([]byte(`"live"`), &access); err != nil {
		t.Fatalf("Unmarshal(mode) error = %v", err)
	}
	if access.Mode == nil || *access.Mode != ExternalWebLive {
		t.Fatalf("mode = %+v", access)
	}
	if err := json.Unmarshal([]byte(`true`), &access); err != nil {
		t.Fatalf("Unmarshal(bool) error = %v", err)
	}
	if access.Boolean == nil || !*access.Boolean {
		t.Fatalf("boolean = %+v", access)
	}
}

func TestOpenAIFileURIAndLimit(t *testing.T) {
	if got := OpenAIFileURI("file-1"); got != "sediment://file-1" {
		t.Fatalf("OpenAIFileURI() = %q", got)
	}
	if err := ValidateOpenAIFileSize("large.bin", OpenAIFileUploadLimitBytes+1); err == nil {
		t.Fatalf("expected size error")
	}
}
