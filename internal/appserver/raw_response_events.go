package appserver

import "codex_go/internal/session"

const experimentalRawEventsExtraKey = "experimental_raw_events"

func threadRecordExperimentalRawEvents(record *session.Record) bool {
	return record != nil && boolFromMap(record.Metadata.Extra, experimentalRawEventsExtraKey)
}
