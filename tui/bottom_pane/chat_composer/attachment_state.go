package chatcomposer

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/attachment_state.rs.

type AttachedImage struct {
	Placeholder string
	Path        string
}

type LocalImageAttachment struct {
	Placeholder string
	Path        string
}

type AttachmentState struct {
	Files                    []string
	LocalImages              []AttachedImage
	RemoteImageURLs          []string
	SelectedRemoteImageIndex *int
}

func (a *AttachmentState) IsEmpty() bool {
	return a == nil || (len(a.Files) == 0 && len(a.LocalImages) == 0 && len(a.RemoteImageURLs) == 0)
}

func (a *AttachmentState) LocalImagePaths() []string {
	if a == nil {
		return nil
	}
	paths := make([]string, len(a.LocalImages))
	for i, image := range a.LocalImages {
		paths[i] = image.Path
	}
	return paths
}

func (a *AttachmentState) LocalImageAttachments() []LocalImageAttachment {
	if a == nil {
		return nil
	}
	out := make([]LocalImageAttachment, len(a.LocalImages))
	for i, image := range a.LocalImages {
		out[i] = LocalImageAttachment{Placeholder: image.Placeholder, Path: image.Path}
	}
	return out
}

func (a *AttachmentState) SetRemoteImageURLs(urls []string, draft *DraftState) {
	if a == nil {
		return
	}
	a.RemoteImageURLs = append([]string(nil), urls...)
	a.SelectedRemoteImageIndex = nil
	a.RelabelLocalImages(draft)
}

func (a *AttachmentState) RemoteImageURLsCopy() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.RemoteImageURLs...)
}

func (a *AttachmentState) TakeRemoteImageURLs(draft *DraftState) []string {
	if a == nil {
		return nil
	}
	urls := append([]string(nil), a.RemoteImageURLs...)
	a.RemoteImageURLs = nil
	a.SelectedRemoteImageIndex = nil
	a.RelabelLocalImages(draft)
	return urls
}

func (a *AttachmentState) ClearRemoteImageURLs(draft *DraftState) {
	if a != nil {
		a.RemoteImageURLs = nil
		a.SelectedRemoteImageIndex = nil
		a.RelabelLocalImages(draft)
	}
}

func (a *AttachmentState) ResetLocalImages(paths []string, draft *DraftState) {
	if a == nil {
		return
	}
	a.LocalImages = nil
	for index, path := range paths {
		a.LocalImages = append(a.LocalImages, AttachedImage{
			Placeholder: LocalImageLabelText(len(a.RemoteImageURLs) + index + 1),
			Path:        path,
		})
	}
	a.SelectedRemoteImageIndex = nil
	a.RelabelLocalImages(draft)
}

func (a *AttachmentState) AttachImage(draft *DraftState, path string) AttachedImage {
	if a == nil {
		return AttachedImage{}
	}
	image := AttachedImage{
		Placeholder: LocalImageLabelText(len(a.RemoteImageURLs) + len(a.LocalImages) + 1),
		Path:        path,
	}
	if draft != nil {
		draft.InsertElement(image.Placeholder)
	}
	a.LocalImages = append(a.LocalImages, image)
	return image
}

func (a *AttachmentState) PruneLocalImagesForSubmission(placeholders []string) {
	if a == nil || len(a.LocalImages) == 0 {
		return
	}
	keep := map[string]bool{}
	for _, placeholder := range placeholders {
		keep[placeholder] = true
	}
	out := a.LocalImages[:0]
	for _, image := range a.LocalImages {
		if keep[image.Placeholder] {
			out = append(out, image)
		}
	}
	a.LocalImages = out
}

func (a *AttachmentState) TakeRecentSubmissionImagesWithPlaceholders() []LocalImageAttachment {
	if a == nil {
		return nil
	}
	out := a.LocalImageAttachments()
	a.LocalImages = nil
	return out
}

func (a *AttachmentState) RemoteImageLines() []string {
	if a == nil {
		return nil
	}
	lines := make([]string, len(a.RemoteImageURLs))
	for i := range a.RemoteImageURLs {
		label := LocalImageLabelText(i + 1)
		if a.SelectedRemoteImageIndex != nil && *a.SelectedRemoteImageIndex == i {
			label = "> " + label
		} else {
			label = "  " + label
		}
		lines[i] = label
	}
	return lines
}

func (a *AttachmentState) ClearRemoteImageSelection() {
	if a != nil {
		a.SelectedRemoteImageIndex = nil
	}
}

func (a *AttachmentState) HandleRemoteImageSelectionKey(key string, draft *DraftState) bool {
	if a == nil || len(a.RemoteImageURLs) == 0 {
		return false
	}
	switch key {
	case "up":
		if a.SelectedRemoteImageIndex != nil {
			next := max(*a.SelectedRemoteImageIndex-1, 0)
			a.SelectedRemoteImageIndex = &next
			return true
		}
		if draft == nil || draft.Cursor == 0 {
			next := len(a.RemoteImageURLs) - 1
			a.SelectedRemoteImageIndex = &next
			return true
		}
	case "down":
		if a.SelectedRemoteImageIndex == nil {
			return false
		}
		if *a.SelectedRemoteImageIndex+1 < len(a.RemoteImageURLs) {
			next := *a.SelectedRemoteImageIndex + 1
			a.SelectedRemoteImageIndex = &next
		} else {
			a.SelectedRemoteImageIndex = nil
		}
		return true
	case "delete", "backspace":
		if a.SelectedRemoteImageIndex == nil {
			return false
		}
		a.removeSelectedRemoteImage(*a.SelectedRemoteImageIndex, draft)
		return true
	}
	return false
}

func (a *AttachmentState) RemoveDeletedLocalPlaceholders(removed []string, draft *DraftState) bool {
	if a == nil || len(removed) == 0 {
		return false
	}
	remove := map[string]bool{}
	for _, payload := range removed {
		remove[payload] = true
	}
	previous := len(a.LocalImages)
	out := a.LocalImages[:0]
	for _, image := range a.LocalImages {
		if !remove[image.Placeholder] {
			out = append(out, image)
		}
	}
	a.LocalImages = out
	changed := len(a.LocalImages) != previous
	if changed {
		a.RelabelLocalImages(draft)
	}
	return changed
}

func (a *AttachmentState) RelabelLocalImages(draft *DraftState) {
	if a == nil {
		return
	}
	for index := range a.LocalImages {
		expected := LocalImageLabelText(len(a.RemoteImageURLs) + index + 1)
		if a.LocalImages[index].Placeholder == expected {
			continue
		}
		current := a.LocalImages[index].Placeholder
		a.LocalImages[index].Placeholder = expected
		if draft != nil {
			draft.ReplaceElementPayload(current, expected)
		}
	}
}

func (a *AttachmentState) removeSelectedRemoteImage(selected int, draft *DraftState) {
	if selected < 0 || selected >= len(a.RemoteImageURLs) {
		a.SelectedRemoteImageIndex = nil
		return
	}
	a.RemoteImageURLs = append(a.RemoteImageURLs[:selected], a.RemoteImageURLs[selected+1:]...)
	if len(a.RemoteImageURLs) == 0 {
		a.SelectedRemoteImageIndex = nil
	} else {
		next := min(selected, len(a.RemoteImageURLs)-1)
		a.SelectedRemoteImageIndex = &next
	}
	a.RelabelLocalImages(draft)
}

func LocalImageLabelText(index int) string {
	if index <= 0 {
		index = 1
	}
	return "[Image #" + formatInt(index) + "]"
}

func formatInt(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
