package appserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"codex_go/model"

	"github.com/google/uuid"
)

// FeedbackSentryDSN mirrors Rust `SENTRY_DSN` (codex-rs/feedback/src/lib.rs).
// The key is the ingest-only public key; the transport never carries a secret.
const FeedbackSentryDSN = "https://ae32ed50620d7a7792c1ce5df38b3e3e@o33249.ingest.us.sentry.io/4510195390611458"

const (
	// FeedbackUploadTimeout mirrors Rust `UPLOAD_TIMEOUT`: one report's event and
	// every attachment share a single five-minute budget.
	FeedbackUploadTimeout = 5 * time.Minute
	// FeedbackDefaultRateLimit mirrors Rust `DEFAULT_RATE_LIMIT`.
	FeedbackDefaultRateLimit = time.Minute
)

const feedbackSentryUserAgent = "codex-feedback"

// feedbackSentryDSN is the parsed form of a Sentry DSN. The envelope URL and the
// auth header are derived from it exactly like `sentry::types::Dsn`.
type feedbackSentryDSN struct {
	scheme    string
	host      string
	prefix    string
	publicKey string
	projectID string
}

func parseFeedbackSentryDSN(raw string) (feedbackSentryDSN, error) {
	const message = "invalid feedback DSN"
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User == nil {
		return feedbackSentryDSN{}, errors.New(message)
	}
	publicKey := parsed.User.Username()
	if publicKey == "" {
		return feedbackSentryDSN{}, errors.New(message)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	projectID := segments[len(segments)-1]
	if projectID == "" {
		return feedbackSentryDSN{}, errors.New(message)
	}
	prefix := ""
	if len(segments) > 1 {
		prefix = "/" + strings.Join(segments[:len(segments)-1], "/")
	}
	return feedbackSentryDSN{
		scheme:    parsed.Scheme,
		host:      parsed.Host,
		prefix:    prefix,
		publicKey: publicKey,
		projectID: projectID,
	}, nil
}

func (d feedbackSentryDSN) envelopeURL() string {
	return fmt.Sprintf("%s://%s%s/api/%s/envelope/", d.scheme, d.host, d.prefix, d.projectID)
}

// authHeader mirrors Sentry's `X-Sentry-Auth` value
// (`Sentry sentry_version=7, sentry_key=..., sentry_client=...`).
func (d feedbackSentryDSN) authHeader(userAgent string) string {
	if userAgent == "" {
		userAgent = feedbackSentryUserAgent
	}
	return fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s, sentry_client=%s", d.publicKey, userAgent)
}

// feedbackEnvelopeHeaders carries the envelope-level fields Go emits. Rust sets
// the same event id on attachment envelopes so they stay linked to the accepted
// event without replaying it.
type feedbackEnvelopeHeaders struct {
	EventID string
}

type feedbackEnvelopeItem struct {
	Type        string
	Filename    string
	ContentType string
	Payload     []byte
}

const (
	feedbackEnvelopeEventType      = "event"
	feedbackEnvelopeAttachmentType = "attachment"
)

func (item feedbackEnvelopeItem) header() map[string]any {
	header := map[string]any{"type": item.Type, "length": len(item.Payload)}
	if item.Type == feedbackEnvelopeAttachmentType {
		if item.Filename != "" {
			header["filename"] = item.Filename
		}
		if item.ContentType != "" {
			header["content_type"] = item.ContentType
		}
	}
	return header
}

// encodeFeedbackEnvelope serializes a Sentry envelope and gzips it with the same
// fast level Rust uses. The decoded byte count is returned so callers can
// enforce the decoded size limits: gzip cannot begin with a JSON header, so
// already-compressed payloads are sent raw when compression does not shrink
// them (Rust `upload::encode_envelope`).
func encodeFeedbackEnvelope(headers feedbackEnvelopeHeaders, items []feedbackEnvelopeItem) ([]byte, int, error) {
	var raw bytes.Buffer
	envelopeHeader := map[string]any{}
	if headers.EventID != "" {
		envelopeHeader["event_id"] = headers.EventID
	}
	encoded, err := json.Marshal(envelopeHeader)
	if err != nil {
		return nil, 0, err
	}
	raw.Write(encoded)
	raw.WriteByte('\n')
	for _, item := range items {
		encoded, err := json.Marshal(item.header())
		if err != nil {
			return nil, 0, err
		}
		raw.Write(encoded)
		raw.WriteByte('\n')
		raw.Write(item.Payload)
		raw.WriteByte('\n')
	}
	decodedBytes := raw.Len()
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, 0, err
	}
	if _, err := writer.Write(raw.Bytes()); err != nil {
		return nil, 0, err
	}
	if err := writer.Close(); err != nil {
		return nil, 0, err
	}
	if compressed.Len() >= decodedBytes {
		return raw.Bytes(), decodedBytes, nil
	}
	return compressed.Bytes(), decodedBytes, nil
}

// FeedbackDelivery is the upstream HTTP outcome, not proof of durable storage in
// Sentry (Rust `FeedbackDelivery`). Confirmed is false when no response arrived.
type FeedbackDelivery struct {
	Status int
	// Accepted reports an HTTP 2xx response.
	Accepted  bool
	Confirmed bool
	// RetryAfter is the longest `Retry-After` cooldown. HasRetryAfter separates a
	// missing header from an explicit zero cooldown, which Rust treats as "no
	// quota imposed".
	RetryAfter    time.Duration
	HasRetryAfter bool
	// SentryQuota is the `X-Sentry-Rate-Limits` cooldown for feedback categories.
	SentryQuota    time.Duration
	HasSentryQuota bool
}

// FeedbackTransport sends persisted feedback envelopes. Rust's
// `FeedbackTransport` uses a redirect-free client and a raised timeout so a slow
// link cannot inherit the interactive upload's budget.
type FeedbackTransport struct {
	HTTP      model.HTTPDoer
	DSN       string
	UserAgent string
	Timeout   time.Duration
}

func (t *FeedbackTransport) userAgent() string {
	if t == nil || t.UserAgent == "" {
		return feedbackSentryUserAgent
	}
	return t.UserAgent
}

func (t *FeedbackTransport) timeout() time.Duration {
	if t == nil || t.Timeout <= 0 {
		return FeedbackUploadTimeout
	}
	return t.Timeout
}

// Send makes one attempt and reports the upstream result. Callers own retry
// decisions and must retain the exact bytes they passed.
func (t *FeedbackTransport) Send(ctx context.Context, body []byte) FeedbackDelivery {
	if t == nil || t.HTTP == nil {
		return FeedbackDelivery{}
	}
	dsn, err := parseFeedbackSentryDSN(t.DSN)
	if err != nil {
		return FeedbackDelivery{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, dsn.envelopeURL(), bytes.NewReader(body))
	if err != nil {
		return FeedbackDelivery{}
	}
	request.Header.Set("X-Sentry-Auth", dsn.authHeader(t.userAgent()))
	// Persisted parts retain their encoding; only gzip magic bytes mean the body
	// is already compressed (Rust `upload::envelope_request`).
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		request.Header.Set("Content-Encoding", "gzip")
	}
	response, err := t.HTTP.Do(request)
	if err != nil || response == nil {
		return FeedbackDelivery{}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	delivery := FeedbackDelivery{
		Status:        response.StatusCode,
		Accepted:      response.StatusCode >= 200 && response.StatusCode < 300,
		Confirmed:     true,
		RetryAfter:    feedbackRetryAfter(response.Header),
		HasRetryAfter: len(response.Header.Values("Retry-After")) > 0,
	}
	if delay, ok := feedbackSentryRateLimitDelay(response.Header); ok {
		delivery.SentryQuota, delivery.HasSentryQuota = delay, true
	}
	return delivery
}

// feedbackRetryAfter parses every `Retry-After` value, preferring the longest
// cooldown. An unparseable value falls back to DEFAULT_RATE_LIMIT, because an
// invalid cooldown is not permission to send immediately.
func feedbackRetryAfter(header http.Header) time.Duration {
	var delay time.Duration
	for _, value := range header.Values("Retry-After") {
		parsed, ok := parseFeedbackRetryAfter(value)
		if !ok {
			parsed = FeedbackDefaultRateLimit
		}
		if parsed > delay {
			delay = parsed
		}
	}
	return delay
}

func parseFeedbackRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second, true
	}
	date, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := time.Until(date)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

// feedbackSentryRateLimitDelay mirrors Rust `sentry_rate_limit_delay`: quotas for
// unrelated categories (transactions, sessions) do not delay feedback, while
// `error` and `attachment` quotas do, and an empty category list applies to all.
func feedbackSentryRateLimitDelay(header http.Header) (time.Duration, bool) {
	var delay time.Duration
	found := false
	for _, value := range header.Values("X-Sentry-Rate-Limits") {
		for _, quota := range strings.Split(value, ",") {
			fields := strings.Split(strings.TrimSpace(quota), ":")
			seconds := ""
			categories := ""
			if len(fields) > 0 {
				seconds = fields[0]
			}
			if len(fields) > 1 {
				categories = fields[1]
			}
			if categories != "" {
				relevant := false
				for _, category := range strings.Split(categories, ";") {
					if category == "error" || category == "attachment" {
						relevant = true
						break
					}
				}
				if !relevant {
					continue
				}
			}
			parsed, ok := parseFeedbackRetryAfter(seconds)
			if !ok {
				parsed = FeedbackDefaultRateLimit
			}
			if !found || parsed > delay {
				delay, found = parsed, true
			}
		}
	}
	return delay, found
}

// feedbackEnvelopeKind selects the retry policy: Rust never replays the core
// event, only diagnostic attachments.
type feedbackEnvelopeKind int

const (
	feedbackEnvelopeEvent feedbackEnvelopeKind = iota
	feedbackEnvelopeAttachment
)

// sendFeedbackEnvelope mirrors Rust `upload::send_envelope`: it keeps every
// attempt inside the report's shared deadline, retries only attachments (at most
// twice, with 250ms/500ms backoff), and marks the report rate-limited whenever
// the upstream imposes a cooldown so later attachments are skipped.
func sendFeedbackEnvelope(
	ctx context.Context,
	transport *FeedbackTransport,
	body []byte,
	kind feedbackEnvelopeKind,
	deadline time.Time,
	rateLimited *bool,
) (FeedbackDelivery, error) {
	retryDelays := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
	attempt := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return FeedbackDelivery{}, errors.New("feedback upload deadline exceeded")
		}
		attemptCtx, cancel := context.WithTimeout(ctx, remaining)
		delivery := transport.Send(attemptCtx, body)
		cancel()
		if delivery.Confirmed {
			// Even accepted requests can impose a quota on events or attachments;
			// quotas for unrelated Sentry categories are ignored upstream.
			if (delivery.HasSentryQuota && delivery.SentryQuota > 0) ||
				(delivery.Accepted && delivery.HasRetryAfter && delivery.RetryAfter > 0) {
				setFeedbackRateLimited(rateLimited)
				return delivery, nil
			}
		}
		retryable := false
		if delivery.Confirmed {
			retryable = delivery.Status == http.StatusRequestTimeout ||
				delivery.Status == http.StatusTooManyRequests ||
				(delivery.Status >= 500 && delivery.Status <= 599)
		}
		if kind == feedbackEnvelopeEvent || !retryable || delivery.Status == 0 {
			return delivery, nil
		}
		if attempt >= len(retryDelays) {
			return delivery, nil
		}
		if delivery.Status == http.StatusTooManyRequests && !delivery.HasRetryAfter {
			// Never let later files bypass an exhausted rate limit.
			setFeedbackRateLimited(rateLimited)
			return delivery, nil
		}
		delay := retryDelays[attempt]
		attempt++
		if delivery.HasRetryAfter && delivery.RetryAfter > delay {
			delay = delivery.RetryAfter
		}
		if delay >= time.Until(deadline) {
			setFeedbackRateLimited(rateLimited)
			return delivery, nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return FeedbackDelivery{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func setFeedbackRateLimited(rateLimited *bool) {
	if rateLimited != nil {
		*rateLimited = true
	}
}

// prepareReportEvent renders the report event with the caller's stable report id
// (Rust `FeedbackSnapshot::prepare_report_event`).
func (s *FeedbackSnapshot) prepareReportEvent(
	reportID string,
	classification string,
	reason *string,
	clientTags map[string]string,
	sessionSource *string,
) ([]byte, string, error) {
	eventID := reportID
	if _, err := uuid.Parse(eventID); err != nil {
		return nil, "", errors.New("invalid feedback report ID")
	}
	event := s.feedbackReportEvent(classification, reason, clientTags, sessionSource, eventID)
	data, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}
	return data, eventID, nil
}

func encodeFeedbackEventEnvelope(payload []byte, eventID string) ([]byte, error) {
	body, decodedBytes, err := encodeFeedbackEnvelope(feedbackEnvelopeHeaders{EventID: eventID}, []feedbackEnvelopeItem{{
		Type:    feedbackEnvelopeEventType,
		Payload: payload,
	}})
	if err != nil {
		return nil, fmt.Errorf("failed to serialize feedback event: %w", err)
	}
	if decodedBytes > FeedbackMaxEventBytes {
		return nil, errors.New("feedback event exceeds the size limit")
	}
	return body, nil
}

func encodeFeedbackAttachmentEnvelope(headers feedbackEnvelopeHeaders, attachment FeedbackAttachment) ([]byte, error) {
	if len(attachment.Buffer) > FeedbackMaxDecodedUploadBytes {
		return nil, errors.New("feedback attachment exceeds the size limit")
	}
	body, decodedBytes, err := encodeFeedbackEnvelope(headers, []feedbackEnvelopeItem{{
		Type:        feedbackEnvelopeAttachmentType,
		Filename:    attachment.Filename,
		ContentType: attachment.ContentType,
		Payload:     attachment.Buffer,
	}})
	if err != nil {
		return nil, fmt.Errorf("failed to serialize feedback attachment: %w", err)
	}
	if decodedBytes > FeedbackMaxDecodedUploadBytes {
		return nil, errors.New("feedback attachment envelope exceeds the size limit")
	}
	return body, nil
}

// UploadFeedback submits the report and its whole attachments within a shared
// deadline (Rust `FeedbackSnapshot::upload_feedback`). Success means HTTP
// acceptance, not durable attachment storage; callers must retain their source
// files because errors can occur after the event was accepted.
func (s *FeedbackSnapshot) UploadFeedback(
	ctx context.Context,
	options *FeedbackUploadOptions,
	transport *FeedbackTransport,
) error {
	if options == nil {
		options = &FeedbackUploadOptions{}
	}
	prepared := s.PrepareUpload(options)
	reportPayload, envelopeEventID, err := s.prepareReportEvent(
		uuid.NewString(),
		options.Classification,
		options.Reason,
		options.ClientTags,
		options.SessionSource,
	)
	if err != nil {
		return err
	}
	eventBody, err := encodeFeedbackEventEnvelope(reportPayload, envelopeEventID)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(transport.timeout())
	rateLimited := false
	delivery, err := sendFeedbackEnvelope(ctx, transport, eventBody, feedbackEnvelopeEvent, deadline, &rateLimited)
	if err != nil {
		return err
	}
	if !delivery.Accepted {
		return fmt.Errorf("Sentry rejected feedback upload with HTTP status %d", delivery.Status)
	}
	attachments := append([]FeedbackAttachment(nil), prepared.Attachments...)
	for _, path := range prepared.AttachmentPaths {
		attachment, ok, err := readFeedbackAttachmentFile(path.Path, path.AttachmentFilenameOverride, FeedbackMaxDecodedUploadBytes)
		if err != nil {
			return err
		}
		if ok {
			attachments = append(attachments, attachment)
		}
	}
	headers := feedbackEnvelopeHeaders{EventID: envelopeEventID}
	attachmentsFailed := false
	for _, attachment := range attachments {
		if rateLimited || !time.Now().Before(deadline) {
			attachmentsFailed = true
			break
		}
		body, err := encodeFeedbackAttachmentEnvelope(headers, attachment)
		if err == nil {
			var delivery FeedbackDelivery
			delivery, err = sendFeedbackEnvelope(ctx, transport, body, feedbackEnvelopeAttachment, deadline, &rateLimited)
			if err == nil && !delivery.Accepted {
				err = fmt.Errorf("Sentry rejected attachment with HTTP status %d", delivery.Status)
			}
		}
		if err != nil {
			// Keep trying other diagnostics before reporting the partial failure.
			attachmentsFailed = true
		}
	}
	if attachmentsFailed {
		return errors.New("feedback report was accepted, but some attachments failed to upload")
	}
	return nil
}

// readFeedbackAttachmentFile mirrors Rust `FeedbackAttachmentPath::read_attachment`:
// missing or non-regular files are skipped, and anything above the decoded limit
// is an error so callers cannot silently truncate a diagnostic.
func readFeedbackAttachmentFile(path string, filenameOverride *string, maxBytes int) (FeedbackAttachment, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FeedbackAttachment{}, false, nil
		}
		return FeedbackAttachment{}, false, nil
	}
	if !info.Mode().IsRegular() {
		return FeedbackAttachment{}, false, nil
	}
	if info.Size() > int64(maxBytes) {
		return FeedbackAttachment{}, false, errors.New("feedback attachment is not a regular file or exceeds the size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FeedbackAttachment{}, false, err
	}
	filename := info.Name()
	if filenameOverride != nil && strings.TrimSpace(*filenameOverride) != "" {
		filename = *filenameOverride
	}
	return FeedbackAttachment{Filename: filename, Buffer: data}, true, nil
}
