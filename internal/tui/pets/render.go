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

type PetImageRenderState struct {
	LastSixelClearArea *SixelClearArea
	LastProtocol       ImageProtocol
}

type SixelClearArea struct {
	X            uint16
	ClearTopY    uint16
	ClearBottomY uint16
	Columns      uint16
}

func RenderAmbientPetImage(writer io.Writer, state *PetImageRenderState, request *AmbientPetDraw, env map[string]string) error {
	return RenderPetImage(writer, state, AmbientPetImageID, request, env)
}

func RenderPetPickerPreviewImage(writer io.Writer, state *PetImageRenderState, request *AmbientPetDraw, env map[string]string) error {
	return RenderPetImage(writer, state, PickerPetImageID, request, env)
}

func RenderPetImage(writer io.Writer, state *PetImageRenderState, imageID uint32, request *AmbientPetDraw, env map[string]string) error {
	if writer == nil {
		return errors.New("pet image writer is nil")
	}
	if state == nil {
		state = &PetImageRenderState{}
	}
	if request == nil {
		if isKittyProtocol(state.LastProtocol) {
			if _, err := io.WriteString(writer, KittyDeleteImage(imageID, env)); err != nil {
				return err
			}
		}
		state.LastProtocol = ""
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

	if isKittyProtocol(state.LastProtocol) || isKittyProtocol(request.Protocol) {
		if _, err := io.WriteString(writer, KittyDeleteImage(imageID, env)); err != nil {
			return err
		}
	}
	state.LastProtocol = request.Protocol

	payload, err := requestPayload(*request, imageID, env)
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
		_, err := writer.Write(payload)
		return err
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

func requestPayload(request AmbientPetDraw, imageID uint32, env map[string]string) ([]byte, error) {
	switch request.Protocol {
	case ImageProtocolKitty:
		payload, err := KittyTransmitPNGWithID(request.Frame, request.Columns, request.Rows, &imageID, env)
		return []byte(payload), err
	case ImageProtocolKittyLocalFile:
		payload, err := KittyTransmitPNGFileWithID(request.Frame, request.Columns, request.Rows, &imageID, env)
		return []byte(payload), err
	case ImageProtocolSixel:
		return os.ReadFile(request.Frame)
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
