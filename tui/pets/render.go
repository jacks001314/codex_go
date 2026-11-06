package pets

import (
	"errors"
	"io"
	"os"
)

const (
	AmbientPetImageID = 0xC0DE
	PickerPetImageID  = 0xC0DF
)

const (
	// AmbientPetPaneSlot and PickerPetPaneSlot are the last two ids of a tmux
	// pane's 256-id image window, which keeps them apart from transcript
	// images (Rust #59580).
	AmbientPetPaneSlot uint32 = 254
	PickerPetPaneSlot  uint32 = 255
)

// PetImageOptions carries the tmux pane state for one pet render.
type PetImageOptions struct {
	// PaneBase is the pane's Kitty image-id base when fullscreen tmux
	// passthrough is enabled, so each pane owns a private id range and the
	// image travels with its placeholder cells.
	PaneBase *uint32
}

type PetImageRenderState struct {
	LastSixelClearArea *SixelClearArea
	// LastKittyID is the id of the last Kitty drawing, which may differ from
	// the caller's id inside a tmux pane.
	LastKittyID *uint32
}

type SixelClearArea struct {
	X            uint16
	ClearTopY    uint16
	ClearBottomY uint16
	Columns      uint16
}

func RenderAmbientPetImage(writer io.Writer, state *PetImageRenderState, request *AmbientPetDraw, env map[string]string, options PetImageOptions) error {
	return RenderPetImage(writer, state, AmbientPetImageID, AmbientPetPaneSlot, request, env, options)
}

func RenderPetPickerPreviewImage(writer io.Writer, state *PetImageRenderState, request *AmbientPetDraw, env map[string]string, options PetImageOptions) error {
	return RenderPetImage(writer, state, PickerPetImageID, PickerPetPaneSlot, request, env, options)
}

func RenderPetImage(writer io.Writer, state *PetImageRenderState, imageID uint32, paneSlot uint32, request *AmbientPetDraw, env map[string]string, options PetImageOptions) error {
	if writer == nil {
		return errors.New("pet image writer is nil")
	}
	if state == nil {
		state = &PetImageRenderState{}
	}
	effectiveID := imageID
	placement := CursorKittyPlacement(imageID)
	if options.PaneBase != nil {
		effectiveID = *options.PaneBase + paneSlot
		placement = VirtualKittyPlacement(effectiveID)
	}
	if request == nil {
		if state.LastKittyID != nil {
			if _, err := io.WriteString(writer, KittyDeleteImage(*state.LastKittyID, env)); err != nil {
				return err
			}
			state.LastKittyID = nil
		}
		if state.LastSixelClearArea != nil {
			if err := writeSavedCursor(writer, func() error {
				return clearSixelArea(writer, *state.LastSixelClearArea)
			}); err != nil {
				return err
			}
			state.LastSixelClearArea = nil
		}
		return nil
	}

	currentID := (*uint32)(nil)
	if isKittyProtocol(request.Protocol) {
		id := effectiveID
		currentID = &id
	}
	// Rust replaces the previous image in the same slot before drawing again.
	deleteID := currentID
	if state.LastKittyID != nil {
		deleteID = state.LastKittyID
	}
	if deleteID != nil {
		if _, err := io.WriteString(writer, KittyDeleteImage(*deleteID, env)); err != nil {
			return err
		}
	}
	state.LastKittyID = currentID

	payload, err := requestPayload(*request, placement, env)
	if err != nil {
		return err
	}
	return writeSavedCursor(writer, func() error {
		currentSixelArea := (*SixelClearArea)(nil)
		if request.Protocol == ImageProtocolSixel {
			area := SixelClearAreaFromDraw(*request)
			currentSixelArea = &area
		}
		if state.LastSixelClearArea != nil && (currentSixelArea == nil || *state.LastSixelClearArea != *currentSixelArea) {
			if err := clearSixelArea(writer, *state.LastSixelClearArea); err != nil {
				return err
			}
		}
		if currentSixelArea != nil {
			if err := clearSixelArea(writer, *currentSixelArea); err != nil {
				return err
			}
			areaCopy := *currentSixelArea
			state.LastSixelClearArea = &areaCopy
		} else {
			state.LastSixelClearArea = nil
		}
		if _, err := io.WriteString(writer, moveTo(request.X, request.Y)); err != nil {
			return err
		}
		if _, err := writer.Write(payload); err != nil {
			return err
		}
		return WriteVirtualKittyPlaceholders(writer, placement, int(request.X), int(request.Y), int(request.Columns), int(request.Rows))
	})
}

func SixelClearAreaFromDraw(request AmbientPetDraw) SixelClearArea {
	return SixelClearArea{
		X:            request.X,
		ClearTopY:    request.ClearTopY,
		ClearBottomY: request.Y + request.Rows,
		Columns:      request.Columns,
	}
}

func requestPayload(request AmbientPetDraw, placement KittyPlacement, env map[string]string) ([]byte, error) {
	switch request.Protocol {
	case ImageProtocolKitty:
		payload, err := KittyTransmitPNGWithID(request.Frame, request.Columns, request.Rows, placement, env)
		return []byte(payload), err
	case ImageProtocolKittyLocalFile:
		payload, err := KittyTransmitPNGFileWithID(request.Frame, request.Columns, request.Rows, placement, env)
		return []byte(payload), err
	case ImageProtocolSixel:
		path, err := PrepareSixelFrame(request.Frame, request.SixelDir, request.HeightPX)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	default:
		return nil, errors.New("unsupported pet image protocol " + string(request.Protocol))
	}
}

func clearSixelArea(writer io.Writer, area SixelClearArea) error {
	blank := make([]byte, area.Columns)
	for i := range blank {
		blank[i] = ' '
	}
	for row := area.ClearTopY; row < area.ClearBottomY; row++ {
		if _, err := io.WriteString(writer, moveTo(area.X, row)); err != nil {
			return err
		}
		if _, err := writer.Write(blank); err != nil {
			return err
		}
	}
	return nil
}

func writeSavedCursor(writer io.Writer, write func() error) error {
	if _, err := io.WriteString(writer, "\x1b7"); err != nil {
		return err
	}
	if err := write(); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\x1b8")
	return err
}

func moveTo(x uint16, y uint16) string {
	return "\x1b[" + uintToString(uint32(y)+1) + ";" + uintToString(uint32(x)+1) + "H"
}

func isKittyProtocol(protocol ImageProtocol) bool {
	return protocol == ImageProtocolKitty || protocol == ImageProtocolKittyLocalFile
}
