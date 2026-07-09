package tui

type GitActionDirective string

const (
	GitActionCommit GitActionDirective = "commit"
	GitActionReview GitActionDirective = "review"
)
