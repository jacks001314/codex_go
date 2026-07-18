package appserver

type connectionServerRequestSink struct {
	connectionID string
	send         func(*ServerRequest)
}

func (s connectionServerRequestSink) SendServerRequest(request *ServerRequest) {
	if s.send != nil {
		s.send(request)
	}
}

func (s connectionServerRequestSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	if normalizeConnectionID(connectionID) != normalizeConnectionID(s.connectionID) {
		return
	}
	s.SendServerRequest(request)
}
