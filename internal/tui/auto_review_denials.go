package tui

type AutoReviewDenial struct {
	ID      string
	Summary string
}

func AutoReviewDenialSummaries(denials []AutoReviewDenial) []string {
	out := make([]string, 0, len(denials))
	for _, denial := range denials {
		if denial.Summary != "" {
			out = append(out, denial.Summary)
		}
	}
	return out
}
