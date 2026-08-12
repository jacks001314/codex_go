package plugin

import (
	"strconv"
	"strings"
)

// Rust parity: codex-rs/core-plugins/src/artifact_operation.rs.

const primaryRuntimeMarketplaceName = "openai-primary-runtime"
const maxExpectedOutputCount = 100

// ArtifactOperation describes a validated create/edit marker command from a
// trusted artifact plugin (Rust #38057).
type ArtifactOperation struct {
	PluginName          string
	ScriptPath          string
	ArtifactType        string
	OperationKind       string
	ExpectedOutputCount int
	OutputFormat        string
}

type artifactSkill struct {
	pluginName    string
	scriptPath    string
	artifactType  string
	outputFormats []string
}

var artifactSkills = []artifactSkill{
	{
		pluginName:    "presentations",
		scriptPath:    "skills/presentations/container_tools/mark_artifact_operation_started.mjs",
		artifactType:  "presentation",
		outputFormats: []string{"ppt", "pptx"},
	},
	{
		pluginName:    "documents",
		scriptPath:    "skills/documents/container_tools/mark_artifact_operation_started.mjs",
		artifactType:  "document",
		outputFormats: []string{"doc", "docx"},
	},
	{
		pluginName:    "spreadsheets",
		scriptPath:    "skills/spreadsheets/container_tools/mark_artifact_operation_started.mjs",
		artifactType:  "spreadsheet",
		outputFormats: []string{"csv", "tsv", "xls", "xlsm", "xlsx"},
	},
	{
		pluginName:    "pdf",
		scriptPath:    "skills/pdf/container_tools/mark_artifact_operation_started.mjs",
		artifactType:  "pdf",
		outputFormats: []string{"pdf"},
	},
}

// RecognizeArtifactOperation validates a trusted primary-runtime artifact
// marker command against the known artifact skills (Rust #38057).
func RecognizeArtifactOperation(attribution *PluginCommandAttribution, command []string) *ArtifactOperation {
	if attribution == nil {
		return nil
	}
	id, err := ParsePluginId(attribution.PluginID)
	if err != nil || id.MarketplaceName != primaryRuntimeMarketplaceName {
		return nil
	}
	var skill *artifactSkill
	for i := range artifactSkills {
		candidate := &artifactSkills[i]
		if id.PluginName == candidate.pluginName && attribution.ScriptPath == candidate.scriptPath {
			skill = candidate
			break
		}
	}
	if skill == nil {
		return nil
	}
	scriptArguments := commandScriptArguments(command)
	if len(scriptArguments) != 6 {
		return nil
	}
	if scriptArguments[0] != "--operation-kind" ||
		scriptArguments[2] != "--expected-output-count" ||
		scriptArguments[4] != "--output-format" {
		return nil
	}
	operationKind := scriptArguments[1]
	if operationKind != "create" && operationKind != "edit" {
		return nil
	}
	expectedOutputCount, err := strconv.Atoi(scriptArguments[3])
	if err != nil || expectedOutputCount < 1 || expectedOutputCount > maxExpectedOutputCount {
		return nil
	}
	outputFormat := scriptArguments[5]
	matched := ""
	for _, known := range skill.outputFormats {
		if strings.EqualFold(known, outputFormat) {
			matched = known
			break
		}
	}
	if matched == "" {
		return nil
	}
	return &ArtifactOperation{
		PluginName:          skill.pluginName,
		ScriptPath:          skill.scriptPath,
		ArtifactType:        skill.artifactType,
		OperationKind:       operationKind,
		ExpectedOutputCount: expectedOutputCount,
		OutputFormat:        matched,
	}
}

// commandScriptArguments returns the tokens following the plugin script path
// in a plain single-command invocation (mirrors the Rust helper).
func commandScriptArguments(command []string) []string {
	flat, ok := singlePlainPluginCommand(command)
	if !ok {
		return nil
	}
	script, ok := pluginScriptArgument(flat)
	if !ok {
		return nil
	}
	for index, token := range flat {
		if token == script {
			return append([]string(nil), flat[index+1:]...)
		}
	}
	return nil
}
