package appserver

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strings"

	"codex_go/model"
)

const appsReplacementNotice = "\nThese Apps usage instructions replace all previously provided Apps usage instructions.\n\n"
const appsRemovalNotice = "\nThe previously provided Apps usage instructions no longer apply.\n"

// appsInstructionsTransition reconciles the latest retained fragment, including
// old boolean checkpoints, with the currently selected wording.
func appsInstructionsTransition(previous json.RawMessage, retained *string, available bool, resolved model.ResolvedAppsInstructions) (json.RawMessage, string) {
	bundled := model.ResolveAppsInstructions(nil, "codex_apps").UsageInstructions
	var oldAvailable bool
	boolSnapshot := json.Unmarshal(previous, &oldAvailable) == nil && string(previous) != "null"
	var legacy struct {
		Instructions *string `json:"instructions"`
		Catalog      bool    `json:"catalog"`
	}
	catalogSnapshot := json.Unmarshal(previous, &legacy) == nil && len(previous) > 0 && strings.TrimSpace(string(previous)) != "null"
	var old *string
	oldCatalog := false
	if retained != nil {
		text := strings.TrimSpace(*retained)
		for _, tag := range []string{"apps_instructions", "catalog_apps_instructions"} {
			open, close := "<"+tag+">", "</"+tag+">"
			if !strings.HasPrefix(text, open) || !strings.HasSuffix(text, close) {
				continue
			}
			body := text[len(open) : len(text)-len(close)]
			literal := legacy.Instructions != nil && *legacy.Instructions == appsInstructionsHash(body)
			if !literal {
				if body == appsRemovalNotice {
					break
				}
				body = strings.TrimPrefix(body, appsReplacementNotice)
			}
			if body == bundled {
				if legacy.Catalog {
					oldCatalog = true
				} else if !resolved.CatalogOverride && ((boolSnapshot && !oldAvailable) || (catalogSnapshot && legacy.Instructions == nil && !legacy.Catalog)) {
					break
				}
			} else {
				oldCatalog = true
			}
			old = &body
			break
		}
	} else if (boolSnapshot && oldAvailable) || legacy.Instructions != nil {
		old = &bundled
	}
	var current *string
	if available && resolved.UsageInstructions != "" {
		current = &resolved.UsageInstructions
	}
	isBundled := current != nil && *current == bundled
	snapshot, _ := json.Marshal(isBundled)
	if resolved.CatalogOverride && isBundled {
		hash := appsInstructionsHash(bundled)
		snapshot, _ = json.Marshal(struct {
			Instructions string `json:"instructions"`
			Catalog      bool   `json:"catalog"`
		}{hash, true})
	}
	if (old == nil && current == nil) || (old != nil && current != nil && *old == *current) {
		return snapshot, ""
	}
	body := ""
	if current != nil {
		body = *current
		if old != nil || strings.HasPrefix(body, appsReplacementNotice) || body == appsRemovalNotice {
			body = appsReplacementNotice + body
		}
	} else if resolved.CatalogOverride || oldCatalog {
		body = appsRemovalNotice
	} else {
		return snapshot, ""
	}
	tag := "catalog_apps_instructions"
	if isBundled {
		tag = "apps_instructions"
	}
	return snapshot, "<" + tag + ">" + body + "</" + tag + ">"
}

func appsInstructionsHash(text string) string {
	// Rust fingerprints the JSON string, not its unquoted contents.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	// Only decode actual separator escapes. A literal backslash-u sequence
	// is encoded as a pair of backslashes and must stay literal.
	normalized := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] == '\\' && i+1 < len(raw) {
			if i+6 <= len(raw) && (string(raw[i:i+6]) == "\\u2028" || string(raw[i:i+6]) == "\\u2029") {
				if raw[i+5] == '8' {
					normalized = append(normalized, []byte("\u2028")...)
				} else {
					normalized = append(normalized, []byte("\u2029")...)
				}
				i += 6
			} else {
				normalized = append(normalized, raw[i:i+2]...)
				i += 2
			}
		} else {
			normalized = append(normalized, raw[i])
			i++
		}
	}
	raw = normalized
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:])
}
