package codexapi

import "time"

// RetryAfter is the earliest time a server advised making a follow-up request.
// Capturing an absolute deadline prevents error propagation and reporting from
// restarting the server's delay.
type RetryAfter struct {
	deadline time.Time
}

// NewRetryAfter captures a server delay relative to the current time.
func NewRetryAfter(delay time.Duration) (RetryAfter, bool) {
	if delay < 0 {
		return RetryAfter{}, false
	}
	return NewRetryAfterAt(time.Now().Add(delay)), true
}

// NewRetryAfterAt captures an already resolved server deadline.
func NewRetryAfterAt(deadline time.Time) RetryAfter {
	return RetryAfter{deadline: deadline}
}

// Deadline returns the captured server deadline.
func (r RetryAfter) Deadline() time.Time {
	return r.deadline
}

// RemainingDelay returns the time left before the deadline. Expired advice
// remains valid advice with a zero delay.
func (r RetryAfter) RemainingDelay() time.Duration {
	return r.remainingDelayAt(time.Now())
}

func (r RetryAfter) remainingDelayAt(now time.Time) time.Duration {
	delay := r.deadline.Sub(now)
	if delay < 0 {
		return 0
	}
	return delay
}
