package exec

import (
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
)

func daybreakProgramTestAgent(programs []string) *model.ResponsesAgentRunner {
	info := model.ModelInfo{Slug: "gpt-5"}
	if programs != nil {
		cyber := make([]model.CyberAccessProgram, 0, len(programs))
		for _, program := range programs {
			cyber = append(cyber, model.CyberAccessProgram(program))
		}
		info.AvailableAccessPrograms = &model.ModelAccessPrograms{Cyber: cyber}
	}
	return &model.ResponsesAgentRunner{
		ModelsManager: model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{info}}),
	}
}

func daybreakProgramTestConfig(daybreak bool, apiKeyPrograms *bool) *config.Config {
	features := map[string]any{}
	if apiKeyPrograms != nil {
		features["api_key_cyber_access_programs"] = *apiKeyPrograms
	}
	return &config.Config{Values: map[string]any{
		"daybreak": daybreak,
		"features": features,
	}}
}

// Rust #49856/#53717: an implicit program comes from the model catalog for
// eligible accounts; Daybreak prefers blue, the off preference selects
// standard, API-key standard selection honors the forwarding feature, and
// missing metadata keeps the server's automatic treatment.
func TestExecDaybreakProgramForTurnLikeRust(t *testing.T) {
	chatGPT := &auth.ResolvedAuth{Auth: auth.AuthDotJSON{AuthMode: "chatgpt"}}
	apiKey := &auth.ResolvedAuth{Auth: auth.AuthDotJSON{OpenAIAPIKey: "sk-test"}}
	enabled := true
	disabled := false
	programs := []string{"standard", "daybreak_blue"}

	cases := []struct {
		name       string
		cfg        *config.Config
		auth       *auth.ResolvedAuth
		agent      model.AgentRunner
		providerID string
		modelID    string
		want       string
		wantErr    string
	}{
		{name: "chatgpt daybreak", cfg: daybreakProgramTestConfig(true, nil), auth: chatGPT, modelID: "gpt-5", want: "daybreak_blue"},
		{name: "chatgpt standard", cfg: daybreakProgramTestConfig(false, nil), auth: chatGPT, modelID: "gpt-5", want: "standard"},
		{name: "api key daybreak", cfg: daybreakProgramTestConfig(true, &disabled), auth: apiKey, modelID: "gpt-5", want: "daybreak_blue"},
		{name: "api key standard allowed", cfg: daybreakProgramTestConfig(false, &enabled), auth: apiKey, modelID: "gpt-5", want: "standard"},
		{name: "api key standard opted out", cfg: daybreakProgramTestConfig(false, &disabled), auth: apiKey, modelID: "gpt-5"},
		{
			name:    "daybreak needs an account",
			cfg:     daybreakProgramTestConfig(true, nil),
			modelID: "gpt-5",
			wantErr: "Daybreak requires a signed-in ChatGPT account or an OpenAI API key",
		},
		{
			name:       "daybreak needs the openai provider",
			cfg:        daybreakProgramTestConfig(true, nil),
			auth:       chatGPT,
			providerID: "bedrock",
			modelID:    "gpt-5",
			wantErr:    "Daybreak requires the OpenAI model provider",
		},
		{name: "other provider without daybreak", cfg: daybreakProgramTestConfig(false, nil), auth: chatGPT, providerID: "bedrock", modelID: "gpt-5"},
		{name: "unlisted model daybreak", cfg: daybreakProgramTestConfig(true, nil), auth: chatGPT, modelID: "unlisted-model"},
		{name: "empty metadata daybreak", cfg: daybreakProgramTestConfig(true, nil), auth: chatGPT, agent: daybreakProgramTestAgent([]string{}), modelID: "gpt-5"},
		{
			name:    "nonempty list without daybreak",
			cfg:     daybreakProgramTestConfig(true, nil),
			auth:    chatGPT,
			agent:   daybreakProgramTestAgent([]string{"standard"}),
			modelID: "gpt-5",
			wantErr: "Daybreak is unavailable for model gpt-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := tc.agent
			if agent == nil {
				agent = daybreakProgramTestAgent(programs)
			}
			providerID := tc.providerID
			if providerID == "" {
				providerID = model.OpenAIProviderID
			}
			got, err := execDaybreakProgramForTurn(tc.cfg, tc.auth, agent, providerID, tc.modelID)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("program = %q, want %q", got, tc.want)
			}
		})
	}
}
