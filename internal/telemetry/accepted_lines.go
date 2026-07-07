package telemetry

import (
	"crypto/sha1"
	"fmt"
	"strings"
)

type AcceptedLineFingerprint struct {
	PathHash string `json:"path_hash"`
	LineHash string `json:"line_hash"`
}

type AcceptedLineFingerprintSummary struct {
	AcceptedAddedLines   uint64                    `json:"accepted_added_lines"`
	AcceptedDeletedLines uint64                    `json:"accepted_deleted_lines"`
	LineFingerprints     []AcceptedLineFingerprint `json:"line_fingerprints"`
}

type AcceptedLineFingerprintEventInput struct {
	EventType            string
	TurnID               string
	ThreadID             string
	ProductSurface       *string
	ModelSlug            *string
	CompletedAt          uint64
	RepoHash             *string
	AcceptedAddedLines   uint64
	AcceptedDeletedLines uint64
	LineFingerprints     []AcceptedLineFingerprint
}

type TrackEventRequest struct {
	Type   string                              `json:"type"`
	Params AcceptedLineFingerprintsEventParams `json:"event_params"`
}

type AcceptedLineFingerprintsEventParams struct {
	EventType            string                    `json:"event_type"`
	TurnID               string                    `json:"turn_id"`
	ThreadID             string                    `json:"thread_id"`
	ProductSurface       *string                   `json:"product_surface,omitempty"`
	ModelSlug            *string                   `json:"model_slug,omitempty"`
	CompletedAt          uint64                    `json:"completed_at"`
	RepoHash             *string                   `json:"repo_hash,omitempty"`
	AcceptedAddedLines   uint64                    `json:"accepted_added_lines"`
	AcceptedDeletedLines uint64                    `json:"accepted_deleted_lines"`
	LineFingerprints     []AcceptedLineFingerprint `json:"line_fingerprints"`
}

func AcceptedLineFingerprintsFromUnifiedDiff(diff string) AcceptedLineFingerprintSummary {
	var currentPath string
	inHunk := false
	var summary AcceptedLineFingerprintSummary
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			currentPath = ""
			inHunk = false
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			continue
		}
		if !inHunk && strings.HasPrefix(line, "+++ ") {
			currentPath = NormalizeDiffPath(strings.TrimPrefix(line, "+++ "))
			continue
		}
		if !inHunk && strings.HasPrefix(line, "--- ") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			summary.AcceptedAddedLines++
			if currentPath != "" {
				if normalized, ok := NormalizeEffectiveLine(strings.TrimPrefix(line, "+")); ok {
					summary.LineFingerprints = append(summary.LineFingerprints, AcceptedLineFingerprint{
						PathHash: FingerprintHash("path", currentPath),
						LineHash: FingerprintHash("line", normalized),
					})
				}
			}
			continue
		}
		if strings.HasPrefix(line, "-") {
			summary.AcceptedDeletedLines++
		}
	}
	return summary
}

func AcceptedLineFingerprintEventRequests(input *AcceptedLineFingerprintEventInput) []TrackEventRequest {
	if input == nil {
		return nil
	}
	return []TrackEventRequest{{
		Type: "codex_accepted_line_fingerprints",
		Params: AcceptedLineFingerprintsEventParams{
			EventType:            input.EventType,
			TurnID:               input.TurnID,
			ThreadID:             input.ThreadID,
			ProductSurface:       input.ProductSurface,
			ModelSlug:            input.ModelSlug,
			CompletedAt:          input.CompletedAt,
			RepoHash:             input.RepoHash,
			AcceptedAddedLines:   input.AcceptedAddedLines,
			AcceptedDeletedLines: input.AcceptedDeletedLines,
			LineFingerprints:     []AcceptedLineFingerprint{},
		},
	}}
}

func FingerprintHash(domain string, value string) string {
	hasher := sha1.New()
	hasher.Write([]byte("file-line-v1\x00"))
	hasher.Write([]byte(domain))
	hasher.Write([]byte("\x00"))
	hasher.Write([]byte(value))
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func NormalizeDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}

func NormalizeEffectiveLine(line string) (string, bool) {
	normalized := strings.Join(strings.Fields(line), " ")
	if len(normalized) <= 3 {
		return "", false
	}
	for _, ch := range normalized {
		if ch == '_' || ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' {
			return normalized, true
		}
	}
	return "", false
}
