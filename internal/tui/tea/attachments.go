package tea

import (
	"fmt"
	"net/url"
	"strings"

	appsapi "codex_go/internal/apps"
	"codex_go/internal/appserver"
	pluginapi "codex_go/internal/plugin"
	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
	chatwidget "codex_go/internal/tui/chatwidget"
)

func (m *Model) applyAttachmentCommand(args string, kind bottompane.AttachmentKind) {
	if m == nil {
		return
	}
	value := strings.TrimSpace(args)
	if value == "" {
		m.notice = "Usage: " + attachmentUsage(kind)
		return
	}
	attachment := bottompane.ComposerAttachment{Kind: kind}
	switch kind {
	case bottompane.AttachmentRemoteImage:
		if parsed, err := url.Parse(value); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			m.notice = "Remote image attachment must be a URL."
			return
		}
		attachment.URL = value
	default:
		path, ok := codextui.NormalizePastedPath(value)
		if !ok {
			m.notice = "Attachment path is invalid."
			return
		}
		attachment.Path = path
	}
	m.attachments = append(m.attachments, attachment)
	m.notice = fmt.Sprintf("Attached %s %s", attachmentKindLabel(kind), attachment.Label())
}

func (m *Model) clearAttachments() {
	if m == nil {
		return
	}
	count := len(m.attachments)
	m.attachments = nil
	if count == 0 {
		m.notice = "No attachments to clear."
		return
	}
	m.notice = fmt.Sprintf("Cleared %d attachment%s.", count, pluralS(count))
}

func (m *Model) ComposerAttachments() []bottompane.ComposerAttachment {
	if m == nil || len(m.attachments) == 0 {
		return nil
	}
	return cloneComposerAttachments(m.attachments)
}

func (m *Model) promptWithAttachments(prompt string) string {
	if m == nil {
		return strings.TrimSpace(prompt)
	}
	return promptWithAttachmentList(prompt, m.attachments)
}

func (m *Model) promptWithRequestAttachments(request SubmitRequest) string {
	return promptWithAttachmentList(request.Prompt, request.Attachments)
}

func promptWithAttachmentList(prompt string, attachments []bottompane.ComposerAttachment) string {
	if len(attachments) == 0 {
		return strings.TrimSpace(prompt)
	}
	lines := []string{strings.TrimSpace(prompt)}
	if lines[0] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, "Attachments:")
	for _, attachment := range attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			lines = append(lines, "- image: "+attachment.Path)
		case bottompane.AttachmentRemoteImage:
			lines = append(lines, "- image_url: "+attachment.URL)
		default:
			lines = append(lines, "- file: "+attachment.Path)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func queuedSubmissionSummary(request SubmitRequest) string {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt != "" {
		return firstLineForQueue(prompt)
	}
	if len(request.Attachments) > 0 {
		return fmt.Sprintf("%d attachment%s", len(request.Attachments), pluralS(len(request.Attachments)))
	}
	return "input"
}

func firstLineForQueue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}

func (m *Model) renderAttachmentLine() string {
	if m == nil || len(m.attachments) == 0 {
		return ""
	}
	labels := make([]string, 0, len(m.attachments))
	for _, attachment := range m.attachments {
		labels = append(labels, attachmentKindLabel(attachment.Kind)+": "+attachment.Label())
	}
	return "Attachments: " + strings.Join(labels, " | ")
}

func attachmentUsage(kind bottompane.AttachmentKind) string {
	switch kind {
	case bottompane.AttachmentImage:
		return "/image PATH"
	case bottompane.AttachmentRemoteImage:
		return "/url-image URL"
	default:
		return "/attach PATH"
	}
}

func attachmentKindLabel(kind bottompane.AttachmentKind) string {
	switch kind {
	case bottompane.AttachmentImage:
		return "image"
	case bottompane.AttachmentRemoteImage:
		return "remote image"
	default:
		return "file"
	}
}

func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func cloneSubmitRequest(request SubmitRequest) SubmitRequest {
	return SubmitRequest{
		Prompt:          request.Prompt,
		Attachments:     cloneComposerAttachments(request.Attachments),
		MentionBindings: append([]string(nil), request.MentionBindings...),
		MentionCatalog:  cloneSubmissionMentionCatalog(request.MentionCatalog),
	}
}

func cloneSubmissionMentionCatalog(catalog chatwidget.SubmissionMentionCatalog) chatwidget.SubmissionMentionCatalog {
	return chatwidget.SubmissionMentionCatalog{
		Skills:  cloneSubmitSkillEntries(catalog.Skills),
		Plugins: append([]pluginapi.PluginSummary(nil), catalog.Plugins...),
		Apps:    append([]appsapi.AppEntry(nil), catalog.Apps...),
	}
}

func cloneSubmitSkillEntries(values []appserver.SkillsListEntry) []appserver.SkillsListEntry {
	if values == nil {
		return nil
	}
	out := make([]appserver.SkillsListEntry, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].Skills = cloneSubmitSkillEntries(values[i].Skills)
		out[i].Errors = append([]appserver.SkillErrorInfo(nil), values[i].Errors...)
	}
	return out
}

func cloneComposerAttachments(values []bottompane.ComposerAttachment) []bottompane.ComposerAttachment {
	if len(values) == 0 {
		return nil
	}
	out := make([]bottompane.ComposerAttachment, len(values))
	copy(out, values)
	return out
}
