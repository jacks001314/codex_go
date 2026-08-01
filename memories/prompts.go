package memories

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/model"
	"codex_go/utils"
)

const (
	DefaultStageOneRolloutTokenLimit = 150_000
	StageOneContextWindowPercent     = 70
	ExtensionsSubdir                 = "extensions"
)

//go:embed templates/stage_one_system.md
var stageOneSystemPrompt string

//go:embed templates/stage_one_input.md
var stageOneInputTemplate string

//go:embed templates/consolidation.md
var consolidationPromptTemplate string

const extensionsFolderStructure = `
Memory extensions (under {{ memory_extensions_root }}/):

- <extension_name>/instructions.md
  - Source-specific guidance for interpreting additional memory signals. If an
    extension folder exists, you must read its instructions.md to determine how to use this memory
    source.

If the user has any memory extensions, you MUST read the instructions for each extension to
determine how to use the memory source. If the workspace diff shows deleted extension resource files,
remove stale memories derived only from those resources. If it has no extension folders, continue
with the standard memory inputs only.
`

const extensionsPrimaryInputs = `
Optional source-specific inputs:
Under ` + "`{{ memory_extensions_root }}/`" + `:

- ` + "`<extension_name>/instructions.md`" + `
  - If extension folders exist, read each instructions.md first and follow it when interpreting
    that extension's memory source.

If the workspace diff shows deleted memory extension resources, use that extension-specific deletion
signal to remove stale memories derived only from those resources.
`

func StageOneSystemPrompt() string {
	return stageOneSystemPrompt
}

func BuildStageOneInputMessage(info model.ModelInfo, rolloutPath, rolloutCWD, rolloutContents string) string {
	limit := resolvedStageOneTokenLimit(info)
	truncated := utils.FormattedTruncateText(rolloutContents, utils.TokensPolicy(limit))
	return renderMemoryTemplate(stageOneInputTemplate, map[string]string{
		"rollout_path":     rolloutPath,
		"rollout_cwd":      rolloutCWD,
		"rollout_contents": truncated,
	})
}

func BuildConsolidationPrompt(root string) string {
	extensionsRoot := filepath.Join(root, ExtensionsSubdir)
	folderBlock := ""
	inputsBlock := ""
	if info, err := os.Stat(extensionsRoot); err == nil && info.IsDir() {
		values := map[string]string{"memory_extensions_root": extensionsRoot}
		folderBlock = renderMemoryTemplate(extensionsFolderStructure, values)
		inputsBlock = renderMemoryTemplate(extensionsPrimaryInputs, values)
	}
	return renderMemoryTemplate(consolidationPromptTemplate, map[string]string{
		"memory_root":                        root,
		"memory_extensions_folder_structure": folderBlock,
		"memory_extensions_primary_inputs":   inputsBlock,
		"phase2_workspace_diff_file":         WorkspaceDiffFilename,
	})
}

func resolvedStageOneTokenLimit(info model.ModelInfo) int {
	window := info.ContextWindow
	if window <= 0 {
		window = info.MaxContextWindow
	}
	if window <= 0 {
		return DefaultStageOneRolloutTokenLimit
	}
	effectivePercent := info.EffectiveContextWindowPercent
	if effectivePercent <= 0 {
		effectivePercent = 100
	}
	limit := window * int64(effectivePercent) / 100
	limit = limit * StageOneContextWindowPercent / 100
	if limit < 1 {
		limit = 1
	}
	maxInt := int64(^uint(0) >> 1)
	if limit > maxInt {
		return int(maxInt)
	}
	return int(limit)
}

func renderMemoryTemplate(source string, values map[string]string) string {
	result := source
	for key, value := range values {
		result = strings.ReplaceAll(result, fmt.Sprintf("{{ %s }}", key), value)
	}
	return result
}
