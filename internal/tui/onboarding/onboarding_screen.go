package onboarding

type StepState string

const (
	StepHidden     StepState = "hidden"
	StepInProgress StepState = "in_progress"
	StepComplete   StepState = "complete"
)

type Screen struct {
	Title string
	Body  []string
	Steps []Step
	Done  bool
	Exit  bool
}

type Step struct {
	ID    string
	State StepState
	Lines []string
}

func NewScreen(title string, steps ...Step) Screen {
	return Screen{Title: title, Steps: append([]Step(nil), steps...)}
}

func (s Screen) CurrentSteps() []Step {
	out := []Step{}
	for _, step := range s.Steps {
		switch step.State {
		case StepHidden:
			continue
		case StepComplete:
			out = append(out, step)
		case StepInProgress:
			out = append(out, step)
			return out
		default:
			out = append(out, step)
			return out
		}
	}
	return out
}

func (s Screen) IsDone() bool {
	if s.Done {
		return true
	}
	for _, step := range s.Steps {
		if step.State == StepInProgress {
			return false
		}
	}
	return true
}

func (s Screen) ShouldExit() bool {
	return s.Exit
}
