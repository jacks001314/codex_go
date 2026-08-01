package appserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"codex_go/rollout"
)

var ErrInvalidFeedbackRequest = errors.New("invalid feedback request")

const FeedbackDiagnosticsAttachmentFilename = "codex-connectivity-diagnostics.txt"
const FeedbackDoctorReportAttachmentFilename = "codex-doctor-report.json"
const FeedbackWindowsSandboxLogAttachmentFilename = "codex-windows-sandbox.log"
const FeedbackMaxTreeThreads = 8

var FeedbackProxyEnvVars = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"}

type FeedbackUploadParams struct {
	Classification string            `json:"classification"`
	Reason         *string           `json:"reason,omitempty"`
	ThreadID       *string           `json:"threadId,omitempty"`
	IncludeLogs    bool              `json:"includeLogs,omitempty"`
	ExtraLogFiles  []string          `json:"extraLogFiles,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

func (p *FeedbackUploadParams) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"classification": p.Classification,
		"reason":         cloneStringPtrAppserver(p.Reason),
		"threadId":       cloneStringPtrAppserver(p.ThreadID),
		"extraLogFiles":  cloneStringSliceForNullable(p.ExtraLogFiles),
		"tags":           cloneStringMapForNullable(p.Tags),
	}
	if p.IncludeLogs {
		payload["includeLogs"] = true
	}
	return json.Marshal(payload)
}

func (p *FeedbackUploadParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidFeedbackRequest)
	}
	if strings.TrimSpace(p.Classification) == "" {
		return fmt.Errorf("%w: classification is required", ErrInvalidFeedbackRequest)
	}
	return nil
}

type FeedbackUploadResponse struct {
	ThreadID string `json:"threadId"`
}

type FeedbackDiagnostic struct {
	Headline string   `json:"headline"`
	Details  []string `json:"details"`
}

type FeedbackDiagnostics struct {
	diagnostics []FeedbackDiagnostic
}

func NewFeedbackDiagnostics(diagnostics []FeedbackDiagnostic) *FeedbackDiagnostics {
	cloned := make([]FeedbackDiagnostic, len(diagnostics))
	for i := range diagnostics {
		cloned[i] = diagnostics[i]
		cloned[i].Details = append([]string(nil), diagnostics[i].Details...)
	}
	return &FeedbackDiagnostics{diagnostics: cloned}
}

func CollectFeedbackDiagnosticsFromPairs(pairs map[string]string) *FeedbackDiagnostics {
	var details []string
	for _, key := range FeedbackProxyEnvVars {
		value, ok := pairs[key]
		if !ok {
			continue
		}
		details = append(details, key+" = "+value)
	}
	if len(details) == 0 {
		return &FeedbackDiagnostics{}
	}
	return NewFeedbackDiagnostics([]FeedbackDiagnostic{{
		Headline: "Proxy environment variables are set and may affect connectivity.",
		Details:  details,
	}})
}

func (d *FeedbackDiagnostics) IsEmpty() bool {
	return d == nil || len(d.diagnostics) == 0
}

func (d *FeedbackDiagnostics) Items() []FeedbackDiagnostic {
	if d == nil {
		return nil
	}
	return NewFeedbackDiagnostics(d.diagnostics).diagnostics
}

func (d *FeedbackDiagnostics) AttachmentText() *string {
	if d == nil || len(d.diagnostics) == 0 {
		return nil
	}
	lines := []string{"Connectivity diagnostics", ""}
	for _, diagnostic := range d.diagnostics {
		lines = append(lines, "- "+diagnostic.Headline)
		for _, detail := range diagnostic.Details {
			lines = append(lines, "  - "+detail)
		}
	}
	text := strings.Join(lines, "\n")
	return &text
}

type FeedbackSnapshot struct {
	Logs         []byte
	Tags         map[string]string
	Diagnostics  *FeedbackDiagnostics
	ThreadID     string
	LastPrepared *PreparedFeedbackUpload
}

type FeedbackAttachment struct {
	Filename    string
	ContentType string
	Buffer      []byte
}

type FeedbackAttachmentPath struct {
	Path                       string
	AttachmentFilenameOverride *string
}

func (s *FeedbackSnapshot) UploadTags(classification string, reason *string, clientTags map[string]string, sessionSource *string) map[string]string {
	tags := map[string]string{
		"thread_id":      s.ThreadID,
		"classification": classification,
	}
	if reason != nil {
		tags["reason"] = *reason
	}
	if sessionSource != nil {
		tags["session_source"] = *sessionSource
	}
	for _, source := range []map[string]string{clientTags, s.Tags} {
		for key, value := range source {
			if isReservedTag(key) {
				continue
			}
			if _, exists := tags[key]; !exists {
				tags[key] = value
			}
		}
	}
	return tags
}

func (s *FeedbackSnapshot) Attachments(includeLogs bool, extra []FeedbackAttachment, logsOverride []byte) []FeedbackAttachment {
	var attachments []FeedbackAttachment
	if includeLogs {
		logs := append([]byte(nil), s.Logs...)
		if logsOverride != nil {
			logs = append([]byte(nil), logsOverride...)
		}
		attachments = append(attachments, FeedbackAttachment{
			Filename:    "codex-logs.log",
			ContentType: "text/plain",
			Buffer:      logs,
		})
	}
	attachments = append(attachments, cloneAttachments(extra)...)
	if text := s.Diagnostics.AttachmentText(); includeLogs && text != nil {
		attachments = append(attachments, FeedbackAttachment{
			Filename:    FeedbackDiagnosticsAttachmentFilename,
			ContentType: "text/plain",
			Buffer:      []byte(*text),
		})
	}
	return attachments
}

type FeedbackUploadOptions struct {
	Classification      string
	Reason              *string
	ClientTags          map[string]string
	SessionSource       *string
	IncludeLogs         bool
	ExtraAttachments    []FeedbackAttachment
	DoctorReport        *FeedbackDoctorReport
	LogsOverride        []byte
	AttachmentPaths     []FeedbackAttachmentPath
	ExtraAttachmentPath []FeedbackAttachmentPath
}

type PreparedFeedbackUpload struct {
	Tags            map[string]string
	Attachments     []FeedbackAttachment
	AttachmentPaths []FeedbackAttachmentPath
}

func (s *FeedbackSnapshot) PrepareUpload(options *FeedbackUploadOptions) *PreparedFeedbackUpload {
	if options == nil {
		options = &FeedbackUploadOptions{}
	}
	if s.Diagnostics == nil {
		s.Diagnostics = &FeedbackDiagnostics{}
	}
	tags := s.UploadTags(options.Classification, options.Reason, options.ClientTags, options.SessionSource)
	extra := cloneAttachments(options.ExtraAttachments)
	if report := options.DoctorReport; report != nil {
		extra = append(extra, report.Attachment.Clone())
		for key, value := range report.Tags {
			if _, exists := tags[key]; !exists && !isReservedTag(key) {
				tags[key] = value
			}
		}
	}
	paths := cloneAttachmentPaths(options.AttachmentPaths)
	paths = append(paths, cloneAttachmentPaths(options.ExtraAttachmentPath)...)
	prepared := &PreparedFeedbackUpload{
		Tags:            tags,
		Attachments:     s.Attachments(options.IncludeLogs, extra, options.LogsOverride),
		AttachmentPaths: DeduplicateFeedbackAttachmentPaths(paths),
	}
	s.LastPrepared = prepared.Clone()
	return prepared
}

type feedbackTurnContext struct {
	TurnID *string `json:"turn_id"`
	Model  string  `json:"model"`
	Effort *string `json:"effort"`
}

type feedbackTurnMetadata struct {
	Model      string
	Effort     string
	PromptHash string
}

func feedbackTurnMetadataFromRollout(path string, turnID *string) (feedbackTurnMetadata, bool) {
	lines, _, err := rollout.Load(path)
	if err != nil {
		return feedbackTurnMetadata{}, false
	}
	promptHash := ""
	for i := range lines {
		if lines[i].Meta == nil || strings.TrimSpace(lines[i].Meta.BaseInstructions) == "" {
			continue
		}
		promptHash = normalizedFeedbackPromptHash(lines[i].Meta.BaseInstructions)
		break
	}
	for index := len(lines) - 1; index >= 0; index-- {
		if len(lines[index].TurnContext) == 0 {
			continue
		}
		var context feedbackTurnContext
		if err := json.Unmarshal(lines[index].TurnContext, &context); err != nil {
			continue
		}
		if turnID != nil && (context.TurnID == nil || *context.TurnID != *turnID) {
			continue
		}
		return feedbackTurnMetadata{
			Model:      context.Model,
			Effort:     feedbackReasoningEffortTag(context.Effort),
			PromptHash: promptHash,
		}, true
	}
	return feedbackTurnMetadata{}, false
}

func applyFeedbackTurnMetadata(tags map[string]string, metadata feedbackTurnMetadata, ok bool) map[string]string {
	if tags == nil {
		tags = map[string]string{}
	}
	delete(tags, "prompt_hash")
	delete(tags, "prompt_version")
	if !ok {
		return tags
	}
	tags["model"] = metadata.Model
	tags["effort"] = metadata.Effort
	if metadata.PromptHash != "" {
		tags["prompt_hash"] = metadata.PromptHash
	}
	return tags
}

func normalizedFeedbackPromptHash(prompt string) string {
	normalized := strings.Join(strings.Fields(prompt), " ")
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

func feedbackReasoningEffortTag(effort *string) string {
	if effort == nil {
		return "None"
	}
	name := strings.ToLower(strings.TrimSpace(*effort))
	variants := map[string]string{
		"none": "None", "minimal": "Minimal", "low": "Low", "medium": "Medium",
		"high": "High", "xhigh": "XHigh", "max": "Max", "ultra": "Ultra",
	}
	if variant, ok := variants[name]; ok {
		return "Some(" + variant + ")"
	}
	return fmt.Sprintf("Some(Custom(%q))", strings.TrimSpace(*effort))
}

func (p *PreparedFeedbackUpload) Clone() *PreparedFeedbackUpload {
	if p == nil {
		return nil
	}
	tags := map[string]string{}
	for key, value := range p.Tags {
		tags[key] = value
	}
	if len(tags) == 0 && p.Tags == nil {
		tags = nil
	}
	return &PreparedFeedbackUpload{
		Tags:            tags,
		Attachments:     cloneAttachments(p.Attachments),
		AttachmentPaths: cloneAttachmentPaths(p.AttachmentPaths),
	}
}

func FeedbackAutoReviewRolloutFilename(threadID string) string {
	return fmt.Sprintf("auto-review-rollout-%s.jsonl", threadID)
}

func FeedbackThreadIDs(root string, descendants []string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	filtered := make([]string, 0, len(descendants))
	for _, id := range descendants {
		if strings.TrimSpace(id) == "" || id == root {
			continue
		}
		filtered = append(filtered, id)
	}
	sort.Strings(filtered)
	keepDescendants := FeedbackMaxTreeThreads - 1
	if keepDescendants < 0 {
		keepDescendants = 0
	}
	if len(filtered) > keepDescendants {
		filtered = filtered[len(filtered)-keepDescendants:]
	}
	out := make([]string, 0, len(filtered)+1)
	out = append(out, root)
	out = append(out, filtered...)
	return out
}

func FeedbackAttachmentPaths(rolloutPaths []string, guardianRolloutPath *string, threadID string, sandboxLogPath *string, extraLogFiles []string) []FeedbackAttachmentPath {
	seen := map[string]bool{}
	var out []FeedbackAttachmentPath
	add := func(path string, override *string) {
		if strings.TrimSpace(path) == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, FeedbackAttachmentPath{Path: path, AttachmentFilenameOverride: cloneString(override)})
	}
	for _, path := range rolloutPaths {
		add(path, nil)
	}
	if guardianRolloutPath != nil {
		override := FeedbackAutoReviewRolloutFilename(threadID)
		add(*guardianRolloutPath, &override)
	}
	if sandboxLogPath != nil {
		override := FeedbackWindowsSandboxLogAttachmentFilename
		add(*sandboxLogPath, &override)
	}
	for _, path := range extraLogFiles {
		add(path, nil)
	}
	return out
}

func DeduplicateFeedbackAttachmentPaths(paths []FeedbackAttachmentPath) []FeedbackAttachmentPath {
	seen := map[string]bool{}
	out := make([]FeedbackAttachmentPath, 0, len(paths))
	for i := range paths {
		path := strings.TrimSpace(paths[i].Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, FeedbackAttachmentPath{
			Path:                       path,
			AttachmentFilenameOverride: cloneString(paths[i].AttachmentFilenameOverride),
		})
	}
	return out
}

type FeedbackRingBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func NewFeedbackRingBuffer(max int) *FeedbackRingBuffer {
	if max <= 0 {
		max = 1
	}
	return &FeedbackRingBuffer{max: max}
}

func (b *FeedbackRingBuffer) Write(data []byte) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(data) >= b.max {
		b.buf = append([]byte(nil), data[len(data)-b.max:]...)
		return len(data)
	}
	needed := len(b.buf) + len(data)
	if needed > b.max {
		drop := needed - b.max
		b.buf = append([]byte(nil), b.buf[drop:]...)
	}
	b.buf = append(b.buf, data...)
	return len(data)
}

func (b *FeedbackRingBuffer) FeedbackSnapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf...)
}

func FeedbackDisplayClassification(classification string) string {
	switch classification {
	case "bug":
		return "Bug"
	case "bad_result":
		return "Bad result"
	case "good_result":
		return "Good result"
	case "safety_check":
		return "Safety check"
	default:
		return "Other"
	}
}

func isReservedTag(key string) bool {
	switch key {
	case "thread_id", "classification", "cli_version", "session_source", "reason":
		return true
	default:
		return false
	}
}

func cloneAttachments(attachments []FeedbackAttachment) []FeedbackAttachment {
	out := make([]FeedbackAttachment, len(attachments))
	for i := range attachments {
		out[i] = attachments[i].Clone()
	}
	return out
}

func (a *FeedbackAttachment) Clone() FeedbackAttachment {
	if a == nil {
		return FeedbackAttachment{}
	}
	out := *a
	out.Buffer = append([]byte(nil), a.Buffer...)
	return out
}

func cloneAttachmentPaths(paths []FeedbackAttachmentPath) []FeedbackAttachmentPath {
	out := make([]FeedbackAttachmentPath, len(paths))
	for i := range paths {
		out[i] = FeedbackAttachmentPath{
			Path:                       paths[i].Path,
			AttachmentFilenameOverride: cloneString(paths[i].AttachmentFilenameOverride),
		}
	}
	return out
}

func cloneStringSliceForNullable(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringMapForNullable(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func FeedbackSortedTagKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
