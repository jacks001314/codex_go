package requestuserinput

// Rust parity subset: codex-rs/tui/src/bottom_pane/request_user_input/layout.rs.

const DesiredSpacersBetweenSections = 2

type Layout struct {
	Width  int
	Height int
}

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

type LayoutSections struct {
	ProgressArea  Rect
	QuestionArea  Rect
	QuestionLines []string
	OptionsArea   Rect
	NotesArea     Rect
	FooterLines   int
}

type LayoutInput struct {
	Area                   Rect
	HasOptions             bool
	NotesVisible           bool
	QuestionLines          []string
	OptionsPreferredHeight int
	OptionsRequiredHeight  int
	NotesPreferredHeight   int
	FooterPreferredHeight  int
}

type layoutPlan struct {
	ProgressHeight      int
	QuestionHeight      int
	SpacerAfterQuestion int
	OptionsHeight       int
	SpacerAfterOptions  int
	NotesHeight         int
	FooterLines         int
}

func LayoutSectionsFor(input LayoutInput) LayoutSections {
	area := input.Area
	if area.Width < 0 {
		area.Width = 0
	}
	if area.Height < 0 {
		area.Height = 0
	}
	questionLines := append([]string(nil), input.QuestionLines...)
	questionHeight := len(questionLines)
	notesVisible := !input.HasOptions || input.NotesVisible
	var plan layoutPlan
	if input.HasOptions {
		plan = layoutWithOptions(input, notesVisible, &questionLines, questionHeight)
	} else {
		plan = layoutWithoutOptions(input, &questionLines, questionHeight)
	}
	progress, question, options, notes := buildLayoutAreas(area, plan)
	return LayoutSections{
		ProgressArea:  progress,
		QuestionArea:  question,
		QuestionLines: questionLines,
		OptionsArea:   options,
		NotesArea:     notes,
		FooterLines:   plan.FooterLines,
	}
}

func layoutWithOptions(input LayoutInput, notesVisible bool, questionLines *[]string, questionHeight int) layoutPlan {
	availableHeight := maxInt(input.Area.Height, 0)
	minOptionsHeight := minInt(availableHeight, 1)
	maxQuestionHeight := availableHeight - minOptionsHeight
	if questionHeight > maxQuestionHeight {
		questionHeight = maxQuestionHeight
		if questionHeight < 0 {
			questionHeight = 0
		}
		*questionLines = (*questionLines)[:minInt(questionHeight, len(*questionLines))]
	}
	return layoutWithOptionsNormal(
		availableHeight,
		questionHeight,
		maxInt(input.NotesPreferredHeight, 0),
		maxInt(input.FooterPreferredHeight, 0),
		notesVisible,
		maxInt(input.OptionsPreferredHeight, 0),
		maxInt(input.OptionsRequiredHeight, 0),
	)
}

func layoutWithOptionsNormal(availableHeight int, questionHeight int, notesPreferredHeight int, footerPreferredHeight int, notesVisible bool, optionsPreferredHeight int, optionsRequiredHeight int) layoutPlan {
	maxOptionsHeight := maxInt(availableHeight-questionHeight, 0)
	minOptionsHeight := minInt(maxOptionsHeight, 1)
	optionsHeight := minInt(optionsPreferredHeight, maxOptionsHeight)
	if optionsHeight < minOptionsHeight {
		optionsHeight = minOptionsHeight
	}
	used := questionHeight + optionsHeight
	remaining := maxInt(availableHeight-used, 0)
	desiredSpacers := DesiredSpacersBetweenSections
	if notesVisible {
		desiredSpacers = 1
	}
	requiredExtra := footerPreferredHeight + 1 + desiredSpacers
	if remaining < requiredExtra {
		deficit := requiredExtra - remaining
		reducible := maxInt(optionsHeight-minOptionsHeight, 0)
		reduceBy := minInt(deficit, reducible)
		optionsHeight -= reduceBy
		remaining += reduceBy
	}
	progressHeight := 0
	if remaining > 0 {
		progressHeight = 1
		remaining--
	}
	if !notesVisible {
		spacerAfterOptions := 0
		if remaining > footerPreferredHeight {
			spacerAfterOptions = 1
			remaining--
		}
		footerLines := minInt(footerPreferredHeight, remaining)
		remaining -= footerLines
		spacerAfterQuestion := 0
		if remaining > 0 {
			spacerAfterQuestion = 1
			remaining--
		}
		growBy := minInt(remaining, maxInt(optionsRequiredHeight-optionsHeight, 0))
		optionsHeight += growBy
		return layoutPlan{
			QuestionHeight:      questionHeight,
			ProgressHeight:      progressHeight,
			SpacerAfterQuestion: spacerAfterQuestion,
			OptionsHeight:       optionsHeight,
			SpacerAfterOptions:  spacerAfterOptions,
			FooterLines:         footerLines,
		}
	}
	footerLines := minInt(footerPreferredHeight, remaining)
	remaining -= footerLines
	spacerAfterQuestion := 0
	if remaining > 0 {
		spacerAfterQuestion = 1
		remaining--
	}
	notesHeight := minInt(notesPreferredHeight, remaining)
	remaining -= notesHeight
	notesHeight += remaining
	return layoutPlan{
		QuestionHeight:      questionHeight,
		ProgressHeight:      progressHeight,
		SpacerAfterQuestion: spacerAfterQuestion,
		OptionsHeight:       optionsHeight,
		NotesHeight:         notesHeight,
		FooterLines:         footerLines,
	}
}

func layoutWithoutOptions(input LayoutInput, questionLines *[]string, questionHeight int) layoutPlan {
	availableHeight := maxInt(input.Area.Height, 0)
	if questionHeight > availableHeight {
		adjusted := minInt(questionHeight, availableHeight)
		*questionLines = (*questionLines)[:minInt(adjusted, len(*questionLines))]
		return layoutPlan{QuestionHeight: adjusted}
	}
	remaining := availableHeight - questionHeight
	notesHeight := minInt(maxInt(input.NotesPreferredHeight, 0), remaining)
	remaining -= notesHeight
	footerLines := minInt(maxInt(input.FooterPreferredHeight, 0), remaining)
	remaining -= footerLines
	progressHeight := 0
	if remaining > 0 {
		progressHeight = 1
		remaining--
	}
	notesHeight += remaining
	return layoutPlan{
		QuestionHeight: questionHeight,
		ProgressHeight: progressHeight,
		NotesHeight:    notesHeight,
		FooterLines:    footerLines,
	}
}

func buildLayoutAreas(area Rect, heights layoutPlan) (Rect, Rect, Rect, Rect) {
	cursorY := area.Y
	progress := Rect{X: area.X, Y: cursorY, Width: area.Width, Height: heights.ProgressHeight}
	cursorY += heights.ProgressHeight
	question := Rect{X: area.X, Y: cursorY, Width: area.Width, Height: heights.QuestionHeight}
	cursorY += heights.QuestionHeight + heights.SpacerAfterQuestion
	options := Rect{X: area.X, Y: cursorY, Width: area.Width, Height: heights.OptionsHeight}
	cursorY += heights.OptionsHeight + heights.SpacerAfterOptions
	notes := Rect{X: area.X, Y: cursorY, Width: area.Width, Height: heights.NotesHeight}
	return progress, question, options, notes
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
