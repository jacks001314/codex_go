package transcript_image

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os"

	"codex_go/tui/pets"
)

// EncodedImage is one bounded thumbnail payload positioned for the terminal.
// Kitty payloads already carry their image ID and placement size; Sixel
// payloads are raw pixel data written at the saved cursor position.
type EncodedImage struct {
	Protocol pets.ImageProtocol
	Payload  []byte
	// TempPath is the retained transport file for KittyLocalFile. Other
	// protocols remove their temporary file once the payload is encoded.
	TempPath string
}

// Encode renders the visible slice of the image as a terminal graphics
// payload. firstRow is the offset into the image's reserved cell rows and
// rows is the number of visible cells.
func (t *TranscriptImage) Encode(x int, y int, firstRow int, rows int, id uint32, env map[string]string) (*EncodedImage, error) {
	if t == nil || t.Pixels == nil {
		return nil, fmt.Errorf("%w: no image pixels", ErrUnsupported)
	}
	top, bottom, ok := Placement{Image: t, Height: int(t.Rows)}.ClipSourceRows(firstRow, rows)
	if !ok {
		return nil, fmt.Errorf("%w: no visible image rows", ErrUnsupported)
	}
	source := t.Pixels.SubImage(image.Rect(0, top, t.Pixels.Bounds().Dx(), bottom)).(*image.RGBA)
	cropped := image.NewRGBA(image.Rect(0, 0, source.Bounds().Dx(), source.Bounds().Dy()))
	copy(cropped.Pix, source.Pix)

	encoded := &EncodedImage{Protocol: t.Protocol}
	switch t.Protocol {
	case pets.ImageProtocolKitty, pets.ImageProtocolKittyLocalFile:
		file, err := os.CreateTemp("", "codex-transcript-*.png")
		if err != nil {
			return nil, err
		}
		path := file.Name()
		encodeErr := png.Encode(file, cropped)
		closeErr := file.Close()
		if encodeErr != nil {
			_ = os.Remove(path)
			return nil, encodeErr
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return nil, closeErr
		}
		transmit := pets.KittyTransmitPNGWithID
		if t.Protocol == pets.ImageProtocolKittyLocalFile {
			transmit = pets.KittyTransmitPNGFileWithID
		}
		command, err := transmit(path, uint16(max(t.Columns, 1)), uint16(max(rows, 1)), &id, env)
		if err != nil {
			_ = os.Remove(path)
			return nil, err
		}
		encoded.Payload = []byte(command)
		if t.Protocol == pets.ImageProtocolKittyLocalFile {
			encoded.TempPath = path
		} else {
			_ = os.Remove(path)
		}
	case pets.ImageProtocolSixel:
		payload, err := pets.EncodeRGBA(cropped.Pix, uint32(cropped.Bounds().Dx()), uint32(cropped.Bounds().Dy()))
		if err != nil {
			return nil, err
		}
		encoded.Payload = payload
	default:
		return nil, fmt.Errorf("%w: protocol %q", ErrUnsupported, t.Protocol)
	}
	return encoded, nil
}

// Write paints the encoded image at the saved cursor position.
func (e *EncodedImage) Write(writer io.Writer, x int, y int) error {
	if e == nil || writer == nil {
		return nil
	}
	if _, err := io.WriteString(writer, "\x1b7"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "\x1b[%d;%dH", max(y, 0)+1, max(x, 0)+1); err != nil {
		return err
	}
	if _, err := writer.Write(e.Payload); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\x1b8")
	return err
}

// Close removes the retained local-file transport, if any.
func (e *EncodedImage) Close() error {
	if e == nil || e.TempPath == "" {
		return nil
	}
	path := e.TempPath
	e.TempPath = ""
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
