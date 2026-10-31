package appserver

import "fmt"

// turnAdmissionMethods mirrors the Rust client requests that take a
// dispatch-time TurnAdmission permit in
// message_processor.rs::dispatch_initialized_client_request.
var turnAdmissionMethods = map[Method]struct{}{
	// Requests that own a runtime change.
	MethodThreadStart:          {},
	MethodThreadFork:           {},
	MethodThreadResume:         {},
	MethodThreadRevert:         {},
	MethodThreadSettingsUpdate: {},
	MethodTurnSettingsUpdate:   {},
	MethodThreadDelete:         {},
	MethodThreadArchive:        {},
	// Requests that start or advance turn work.
	MethodThreadSample:         {},
	MethodThreadGuardianReview: {},
	MethodTurnStart:            {},
	MethodTurnSteer:            {},
	MethodReviewStart:          {},
	MethodThreadCompactStart:   {},
	MethodThreadShellCommand:   {},
	MethodThreadQueueStart:     {},
	MethodThreadRealtimeStart:  {},
}

func requestTakesTurnAdmission(method Method) bool {
	_, ok := turnAdmissionMethods[method]
	return ok
}

// admitDispatchedRequest takes the admission permit for a request a transport
// just read (Rust message_processor.rs takes TurnAdmission::admit before the
// handler is queued). Requests admitted here keep working after the connection
// starts draining; only requests read after the gate closed are rejected.
func (router *RuntimeRouter) admitDispatchedRequest(request *Request) error {
	if router == nil || request == nil || router.threads == nil {
		return nil
	}
	if !requestTakesTurnAdmission(request.Method) {
		return nil
	}
	permit, err := router.threads.AdmitTurnWork()
	if err != nil {
		return err
	}
	request.turnAdmission = permit
	return nil
}

// turnAdmissionErrorResponse builds the JSON-RPC error Rust sends back to the
// connection that dispatched a request after turn admission closed.
func turnAdmissionErrorResponse(request *Request, err error) *Response {
	return ErrorResponse(request.ID, runtimeErrorCode(err), err.Error(), jsonRPCErrorData(err))
}

// reserveRuntimeThreadForRequest reserves the runtime turn for a dispatched
// request, keeping the permit it took at dispatch so a drain that began while
// the request was preparing does not cancel it.
func (router *RuntimeRouter) reserveRuntimeThreadForRequest(request *Request, threadID string) error {
	if router == nil || router.threads == nil {
		return fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	var permit *TurnAdmissionPermit
	if request != nil {
		permit = request.turnAdmission
	}
	return router.threads.ReserveTurnAdmitted(threadID, permit)
}
