package parity

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"codex_go/audioutil"
)

// TestRustAudioPreparationConstantsAgainstGo pins the model-input audio
// preparation contract against Rust codex-utils-audio: placeholder texts,
// canonical media types, the decoded size limit, and the duration token rate.
func TestRustAudioPreparationConstantsAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "utils", "audio", "src", "lib.rs")))

	placeholders := map[string]string{
		"processing":  audioutil.PlaceholderProcessingError,
		"too large":   audioutil.PlaceholderTooLarge,
		"unsupported": audioutil.PlaceholderUnsupported,
	}
	for name, placeholder := range placeholders {
		if !strings.Contains(source, `"`+placeholder+`"`) {
			t.Fatalf("Rust utils/audio is missing the %s placeholder %q", name, placeholder)
		}
	}

	rate := regexp.MustCompile(`AUDIO_TOKENS_PER_SECOND: f64 = ([0-9.]+);`).FindStringSubmatch(source)
	if rate == nil {
		t.Fatal("Rust AUDIO_TOKENS_PER_SECOND not found")
	}
	tokensPerSecond, err := strconv.ParseFloat(rate[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	// 2 seconds of 8 kHz mono 16-bit PCM must estimate to ceil(2 * rate).
	wav := wavDataURLForParity()
	want := int(tokensPerSecond * 2)
	if got := audioutil.EstimateAudioTokenCount(wav); got != want {
		t.Fatalf("token estimate for 2 s = %d, want %d (rate %v)", got, want, tokensPerSecond)
	}

	// Canonical media types: every alias in Rust's canonical_audio_mime must map
	// to the same canonical value in Go.
	raw, err := os.ReadFile(filepath.Join(root, "protocol", "src", "local_media.rs"))
	if err == nil {
		limitPattern := regexp.MustCompile(`MAX_PROMPT_AUDIO_INPUT_BYTES: usize = ([0-9]+) \* ([0-9]+) \* ([0-9]+);`)
		match := limitPattern.FindStringSubmatch(string(raw))
		if match == nil {
			t.Fatal("Rust MAX_PROMPT_AUDIO_INPUT_BYTES not found")
		}
		product := 1
		for _, factor := range match[1:] {
			value, convErr := strconv.Atoi(factor)
			if convErr != nil {
				t.Fatal(convErr)
			}
			product *= value
		}
		if audioutil.MaxPromptAudioInputBytes != product {
			t.Fatalf("MaxPromptAudioInputBytes = %d, Rust = %d", audioutil.MaxPromptAudioInputBytes, product)
		}
	} else {
		t.Logf("skipping size-limit pin: %v", err)
	}
}

// TestRustAudioCanonicalMIMEMatchesGo compares the alias table parsed from the
// Rust source with the Go mapping.
func TestRustAudioCanonicalMIMEMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "utils", "audio", "src", "lib.rs")))
	start := strings.Index(source, "fn canonical_audio_mime")
	if start < 0 {
		t.Fatal("Rust canonical_audio_mime not found")
	}
	body := source[start:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	branch := regexp.MustCompile(`(?s)if ([^{]*?)\{\s*Some\("([^"]+)"\)`)
	aliases := regexp.MustCompile(`"([^"]+)"`)
	checked := 0
	for _, match := range branch.FindAllStringSubmatch(body, -1) {
		canonical := match[2]
		for _, aliasMatch := range aliases.FindAllStringSubmatch(match[1], -1) {
			alias := aliasMatch[1]
			if !strings.HasPrefix(alias, "audio/") {
				continue
			}
			got, ok := audioutil.CanonicalAudioMIME(alias)
			if !ok || got != canonical {
				t.Fatalf("CanonicalAudioMIME(%q) = %q/%v, Rust maps it to %q", alias, got, ok, canonical)
			}
			checked++
		}
	}
	if checked < 8 {
		t.Fatalf("only %d canonical media aliases were checked", checked)
	}
}

func wavDataURLForParity() string {
	// 2 seconds of 8 kHz mono 16-bit PCM: byteRate 16000, data 32000 bytes.
	const dataSize = 32000
	buffer := make([]byte, 0, 44+dataSize)
	buffer = append(buffer, "RIFF"...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, 36+dataSize)
	buffer = append(buffer, size...)
	buffer = append(buffer, "WAVEfmt "...)
	binary.LittleEndian.PutUint32(size, 16)
	buffer = append(buffer, size...)
	buffer = append(buffer, 1, 0, 1, 0)
	binary.LittleEndian.PutUint32(size, 8000)
	buffer = append(buffer, size...)
	binary.LittleEndian.PutUint32(size, 16000)
	buffer = append(buffer, size...)
	buffer = append(buffer, 2, 0, 16, 0)
	buffer = append(buffer, "data"...)
	binary.LittleEndian.PutUint32(size, dataSize)
	buffer = append(buffer, size...)
	buffer = append(buffer, make([]byte, dataSize)...)
	return "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(buffer)
}
