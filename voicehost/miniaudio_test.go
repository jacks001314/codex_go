package voicehost

import (
	"context"
	"encoding/hex"
	"io"
	"testing"
	"time"
)

func TestPCMBufferReadDrainAndClose(t *testing.T) {
	buffer := newPCMBuffer(16)
	buffer.Append([]byte{1, 2, 3, 4})
	readContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output := make([]byte, 3)
	count, err := buffer.Read(readContext, output)
	if err != nil || count != 3 || !equalBytes(output, []byte{1, 2, 3}) {
		t.Fatalf("read = %d, %v, %v", count, err, output)
	}
	buffer.Append([]byte{5, 6, 7, 8, 9})
	// After the read only [4] remains, so appending [5..9] keeps all six bytes.
	drained := make([]byte, 8)
	count = buffer.Drain(drained)
	if count != 6 || !equalBytes(drained, []byte{4, 5, 6, 7, 8, 9, 0, 0}) {
		t.Fatalf("drain = %d, %v", count, drained)
	}
	buffer.Close()
	if _, err := buffer.Read(readContext, output); err != io.EOF {
		t.Fatalf("closed read = %v", err)
	}
}

func TestPCMBufferReadBlocksUntilDataOrContext(t *testing.T) {
	buffer := newPCMBuffer(16)
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buffer.Read(canceledContext, make([]byte, 1)); err != context.Canceled {
		t.Fatalf("cancelled read = %v", err)
	}
}

func TestDecodeDeviceIDRoundTrip(t *testing.T) {
	deviceID, err := decodeDeviceID("010203ff")
	if err != nil {
		t.Fatal(err)
	}
	if deviceID.String() != "010203ff" {
		t.Fatalf("device id string = %q", deviceID.String())
	}
	decoded, err := hex.DecodeString(deviceID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 4 || !equalBytes(decoded, []byte{1, 2, 3, 0xff}) {
		t.Fatalf("decoded = %v", decoded)
	}
	if _, err := decodeDeviceID("not-hex"); err == nil {
		t.Fatal("invalid device id was accepted")
	}
}

func TestPCMBufferCapacityForFormat(t *testing.T) {
	format := AudioFormat{SampleRate: 24000, Channels: 1, Encoding: AudioEncodingS16LE}
	if got := pcmBufferBytes(format, 2*time.Second); got != 96000 {
		t.Fatalf("capacity = %d", got)
	}
	if got := pcmBufferBytes(AudioFormat{SampleRate: 1000, Channels: 1}, time.Second); got != 64*1024 {
		t.Fatalf("minimum capacity = %d", got)
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
