package tui

type BacktrackAvailability struct {
	Available bool
	Reason    string
}

func BacktrackUnavailable(reason string) BacktrackAvailability {
	return BacktrackAvailability{Reason: reason}
}
