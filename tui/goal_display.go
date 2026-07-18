package tui

type GoalDisplay struct {
	Objective string
	Status    string
}

func (g GoalDisplay) Line() string {
	if g.Status == "" {
		return g.Objective
	}
	return g.Status + ": " + g.Objective
}
