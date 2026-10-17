package codemode

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCodeModeAudioOmitsShortWAVLikeRust(t *testing.T) {
	for _, test := range []struct {
		sampleRate uint32
		frames     int
		omitted    bool
	}{
		{24000, 0, true},
		{24000, 1, true},
		{24000, 599, true},
		{24000, 600, false},
		{24000, 601, false},
		{44100, 223, true},
		{44100, 1102, true},
		{44100, 1103, false},
		{30870, 223, true},
		{30870, 771, true},
		{30870, 772, false},
		{34398, 221, true},
		{34398, 859, true},
		{34398, 860, false},
	} {
		name := fmt.Sprintf("%dHz/%dframes", test.sampleRate, test.frames)
		t.Run(name, func(t *testing.T) {
			payload := base64.StdEncoding.EncodeToString(pcm16WAV(test.sampleRate, test.frames))
			audioURL := "data:audio/wav;base64," + payload
			source := fmt.Sprintf(
				`audio(%q); audio({audio_url:%q}); audio({type:"audio",mimeType:"audio/wav",data:%q});`,
				audioURL, audioURL, payload,
			)
			result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: source})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			want := InputAudio(audioURL)
			if test.omitted {
				want = InputText(shortAudioOmissionText)
			}
			if len(result.ContentItems) != 3 {
				t.Fatalf("items = %#v, want three", result.ContentItems)
			}
			for i, got := range result.ContentItems {
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("item %d = %#v, want %#v", i, got, want)
				}
			}
		})
	}
}

func TestCodeModeAudioPreservesOtherOutputAndUnknownFormatsLikeRust(t *testing.T) {
	shortPayload := base64.StdEncoding.EncodeToString(pcm16WAV(24000, 1))
	shortURL := "data:audio/wav;base64," + shortPayload
	result, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: fmt.Sprintf(`
		text("before");
		audio(%q);
		audio("data:audio/mpeg;base64,YQ==");
		audio("data:audio/wav;base64,not-base64");
		text("after");
	`, shortURL)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []ContentItem{
		InputText("before"),
		InputText(shortAudioOmissionText),
		InputAudio("data:audio/mpeg;base64,YQ=="),
		InputAudio("data:audio/wav;base64,not-base64"),
		InputText("after"),
	}
	if !reflect.DeepEqual(result.ContentItems, want) {
		t.Fatalf("items = %#v, want %#v", result.ContentItems, want)
	}
}

func TestCodeModeAudioRejectsInvalidArgumentsLikeRust(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{`audio()`, audioHelperExpectsMessage},
		{`audio("http://example.com/audio.wav")`, invalidAudioURLMessage},
		{`audio({audio_url: 2})`, audioHelperExpectsMessage},
		{`audio({type:"image",data:"YQ=="})`, `audio only accepts MCP audio blocks, got "image"`},
		{`audio({type:"audio"})`, "audio expected MCP audio data"},
		{`audio({type:"audio",data:2})`, "audio expected MCP audio data"},
		{`audio([])`, audioHelperExpectsMessage},
	} {
		_, err := NewSobekEngine().Execute(context.Background(), EngineRequest{Source: test.source})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("source %s error = %v, want %q", test.source, err, test.want)
		}
	}
}

func TestWAVDurationUsesPresentBytesAndChunkAlignmentLikeRust(t *testing.T) {
	valid := pcm16WAV(24000, 600)
	list := []byte("LIST")
	list = binary.LittleEndian.AppendUint32(list, 12)
	list = append(list, []byte("INFOINAM\x00\x00\x00\x00")...)
	valid = append(valid[:36], append(list, valid[36:]...)...)
	binary.LittleEndian.PutUint32(valid[4:8], uint32(len(valid)-8))

	// A streaming header may declare more data than the file contains. Duration
	// comes from the bytes actually present.
	streaming := append([]byte(nil), valid...)
	dataSizeOffset := 36 + len(list) + 4
	binary.LittleEndian.PutUint32(streaming[dataSizeOffset:dataSizeOffset+4], 48000)
	audioURL := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(streaming)
	duration, ok := wavDurationSeconds(audioURL)
	if !ok || duration < 0.025 || duration > 0.026 {
		t.Fatalf("duration = %v, ok = %v, want about 25 ms", duration, ok)
	}
}

func pcm16WAV(sampleRate uint32, frames int) []byte {
	dataSize := uint32(frames * 2)
	wav := []byte("RIFF")
	wav = binary.LittleEndian.AppendUint32(wav, 36+dataSize)
	wav = append(wav, []byte("WAVEfmt ")...)
	wav = binary.LittleEndian.AppendUint32(wav, 16)
	wav = binary.LittleEndian.AppendUint16(wav, 1)
	wav = binary.LittleEndian.AppendUint16(wav, 1)
	wav = binary.LittleEndian.AppendUint32(wav, sampleRate)
	wav = binary.LittleEndian.AppendUint32(wav, sampleRate*2)
	wav = binary.LittleEndian.AppendUint16(wav, 2)
	wav = binary.LittleEndian.AppendUint16(wav, 16)
	wav = append(wav, []byte("data")...)
	wav = binary.LittleEndian.AppendUint32(wav, dataSize)
	return append(wav, make([]byte, frames*2)...)
}
