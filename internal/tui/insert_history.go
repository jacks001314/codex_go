package tui

type InsertHistoryRequest struct {
	Lines []string
}

func NewInsertHistoryRequest(lines ...string) InsertHistoryRequest {
	return InsertHistoryRequest{Lines: append([]string(nil), lines...)}
}
