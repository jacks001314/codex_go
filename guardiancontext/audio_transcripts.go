// Audio transcripts turn completed speech into ordinary, source-attributed
// Guardian evidence, so they take part in the same delta selection and
// retention as messages and tool results (Rust #63368).

package guardiancontext

import (
	"fmt"
	"strconv"
	"strings"
)

// ConversationTranscriptOptions controls which optional evidence a transcript
// collection admits (Rust ConversationTranscriptOptions).
type ConversationTranscriptOptions struct {
	// IncludeToolOutputs admits tool results; when false a tool audio
	// transcript is dropped instead of being reviewed.
	IncludeToolOutputs bool
}

// AudioTranscriptSource distinguishes the history item that produced completed
// speech.
type AudioTranscriptSource int

const (
	// AudioTranscriptUserInput is speech submitted on a user turn.
	AudioTranscriptUserInput AudioTranscriptSource = iota
	// AudioTranscriptToolOutput is speech returned by a tool call.
	AudioTranscriptToolOutput
)

// AudioTranscript is completed speech paired with the history item that
// produced it (Rust AudioTranscript).
type AudioTranscript struct {
	// Source selects the user-turn or tool-call variant.
	Source AudioTranscriptSource
	// TurnID names the user turn a recording was submitted on. It is read only
	// for AudioTranscriptUserInput.
	TurnID string
	// CallID names the tool call that returned the audio. It is read only for
	// AudioTranscriptToolOutput.
	CallID string
	// Text is the completed transcription.
	Text string
}

func userAudioTranscriptRole(turnID string) string {
	return fmt.Sprintf("audio transcript (recording submitted on user turn %s)", strconv.Quote(turnID))
}

func toolAudioTranscriptRole(callID string) string {
	return fmt.Sprintf("tool audio transcript (call %s)", strconv.Quote(callID))
}

// CollectAudioTranscript converts completed speech into one Guardian transcript
// entry. It returns nil for tool audio when tool outputs are excluded and for
// blank transcriptions, matching Rust collect_audio_transcript.
func CollectAudioTranscript(transcript AudioTranscript, options ConversationTranscriptOptions) *ConversationTranscriptEntry {
	var kind ConversationTranscriptEntryKind
	switch transcript.Source {
	case AudioTranscriptUserInput:
		kind = UserAudioTranscriptKind(transcript.TurnID)
	case AudioTranscriptToolOutput:
		if !options.IncludeToolOutputs {
			return nil
		}
		kind = ToolAudioTranscriptKind(transcript.CallID)
	default:
		return nil
	}
	if strings.TrimSpace(transcript.Text) == "" {
		return nil
	}
	return &ConversationTranscriptEntry{
		Kind:          kind,
		Text:          transcript.Text,
		OriginalBytes: len(transcript.Text),
	}
}
