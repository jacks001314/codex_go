// Package audioutil prepares model-input audio: it canonicalizes data URLs,
// replaces unusable inputs with text placeholders, and estimates token counts
// from decoded duration. It mirrors the Rust codex-utils-audio crate.
package audioutil

import (
	"container/list"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// MaxPromptAudioInputBytes bounds one decoded audio payload (50 MiB), matching
// Rust MAX_PROMPT_AUDIO_INPUT_BYTES.
const MaxPromptAudioInputBytes = 50 * 1024 * 1024

// maxPromptAudioBase64Bytes bounds the encoded payload so a data URL cannot
// exceed the decoded limit by more than base64 expansion.
const maxPromptAudioBase64Bytes = (MaxPromptAudioInputBytes + 2) / 3 * 4

// tokensPerSecond is the audio token estimate rate (Rust AUDIO_TOKENS_PER_SECOND).
const tokensPerSecond = 10.0

// tokenEstimateCacheSize bounds the duration-estimate cache.
const tokenEstimateCacheSize = 32

// Placeholder texts substituted for audio that cannot be sent to the model.
const (
	PlaceholderProcessingError = "audio content omitted because it could not be processed"
	PlaceholderTooLarge        = "audio content omitted because it exceeded the supported size limit; use a smaller audio file"
	PlaceholderUnsupported     = "audio content omitted because its format is not supported; use wav, mp3, m4a, webm, or ogg"
)

// ErrInvalidDataURL reports an audio URL that is not a usable data URL.
var ErrInvalidDataURL = errors.New("invalid audio data URL")

// ErrUnsupportedFormat reports a media type outside the supported set.
var ErrUnsupportedFormat = errors.New("unsupported audio format")

// ErrAudioTooLarge reports a payload beyond the supported size.
var ErrAudioTooLarge = errors.New("audio input is too large")

// Placeholder returns the user-visible text for a preparation error.
func Placeholder(err error) string {
	switch {
	case errors.Is(err, ErrUnsupportedFormat):
		return PlaceholderUnsupported
	case errors.Is(err, ErrAudioTooLarge):
		return PlaceholderTooLarge
	default:
		return PlaceholderProcessingError
	}
}

// CanonicalAudioMIME maps a media type to the canonical form the model accepts.
func CanonicalAudioMIME(mime string) (string, bool) {
	switch {
	case equalFoldAny(mime, "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave"):
		return "audio/wav", true
	case equalFoldAny(mime, "audio/mpeg", "audio/mp3"):
		return "audio/mpeg", true
	case equalFoldAny(mime, "audio/mp4", "audio/m4a", "audio/x-m4a"):
		return "audio/mp4", true
	case strings.EqualFold(mime, "audio/webm"):
		return "audio/webm", true
	case strings.EqualFold(mime, "audio/ogg"):
		return "audio/ogg", true
	default:
		return "", false
	}
}

func equalFoldAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

// IsDataURL reports whether the audio URL uses the data: scheme.
func IsDataURL(audioURL string) bool {
	if len(audioURL) < len("data:") {
		return false
	}
	return strings.EqualFold(audioURL[:len("data:")], "data:")
}

// PrepareAudioURL validates and canonicalizes one audio data URL. On success it
// returns the canonical `data:<mime>;base64,<payload>` form.
func PrepareAudioURL(audioURL string) (string, error) {
	if !IsDataURL(audioURL) {
		return "", fmt.Errorf("%w: audio input must be a data URL", ErrInvalidDataURL)
	}
	metadata, payload, ok := strings.Cut(audioURL, ",")
	if !ok {
		return "", fmt.Errorf("%w: missing payload separator", ErrInvalidDataURL)
	}
	if len(metadata) < len("data:") {
		return "", fmt.Errorf("%w: missing data URL prefix", ErrInvalidDataURL)
	}
	metadata = metadata[len("data:"):]
	parts := strings.Split(metadata, ";")
	mime := strings.TrimSpace(parts[0])
	if mime == "" {
		return "", fmt.Errorf("%w: missing media type", ErrInvalidDataURL)
	}
	canonicalMIME, ok := CanonicalAudioMIME(mime)
	if !ok {
		return "", ErrUnsupportedFormat
	}
	base64Encoded := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			base64Encoded = true
		}
	}
	if !base64Encoded {
		return "", fmt.Errorf("%w: audio payload is not base64 encoded", ErrInvalidDataURL)
	}
	if len(payload) > maxPromptAudioBase64Bytes {
		return "", fmt.Errorf("%w (%d bytes; max %d bytes)", ErrAudioTooLarge, len(payload), MaxPromptAudioInputBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("%w: invalid base64 payload", ErrInvalidDataURL)
	}
	if len(decoded) == 0 {
		return "", fmt.Errorf("%w: audio payload is empty", ErrInvalidDataURL)
	}
	if len(decoded) > MaxPromptAudioInputBytes {
		return "", fmt.Errorf("%w (%d bytes; max %d bytes)", ErrAudioTooLarge, len(decoded), MaxPromptAudioInputBytes)
	}
	return "data:" + canonicalMIME + ";base64," + base64.StdEncoding.EncodeToString(decoded), nil
}

// EstimateAudioTokenCount returns the model token estimate for an audio URL:
// duration-based when the container can be read, otherwise a byte-size
// approximation. Results are cached by URL.
func EstimateAudioTokenCount(audioURL string) int {
	if cached, ok := audioTokenCache.get(audioURL); ok {
		return cached
	}
	estimate := approxTokenCount(audioURL)
	if seconds, ok := DurationSeconds(audioURL); ok {
		tokens := seconds * tokensPerSecond
		if tokens >= float64(maxInt) {
			estimate = maxInt
		} else {
			estimate = int(ceil(tokens))
		}
	}
	audioTokenCache.put(audioURL, estimate)
	return estimate
}

const maxInt = int(^uint(0) >> 1)

func ceil(value float64) float64 {
	truncated := float64(int64(value))
	if value > truncated {
		return truncated + 1
	}
	return truncated
}

// approxTokenCount mirrors Rust codex-utils-string: ceil(bytes / 4).
func approxTokenCount(text string) int {
	return (len(text) + 3) / 4
}

// audioTokenCache is a fixed-size LRU keyed by audio URL.
var audioTokenCache = newLRU(tokenEstimateCacheSize)

type lruCache struct {
	mu      sync.Mutex
	limit   int
	order   *list.List
	entries map[string]*list.Element
}

type lruEntry struct {
	key   string
	value int
}

func newLRU(limit int) *lruCache {
	if limit <= 0 {
		limit = 1
	}
	return &lruCache{limit: limit, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *lruCache) get(key string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return 0, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*lruEntry).value, true
}

func (c *lruCache) put(key string, value int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value.(*lruEntry).value = value
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(&lruEntry{key: key, value: value})
	c.entries[key] = element
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*lruEntry).key)
	}
}
