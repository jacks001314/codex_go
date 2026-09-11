package appserver

import "fmt"

// Runtime handlers for the experimental user-verification RPCs. Go has no
// native provider, so every mutating operation reports the typed
// `unavailable` error and status reports the provider-unavailable state, which
// matches a Rust build whose platform provider is unsupported.

func isUserVerificationMethod(method Method) bool {
	switch method {
	case MethodUserVerificationStatus, MethodUserVerificationEnroll,
		MethodUserVerificationDelete, MethodUserVerificationVerify,
		MethodUserVerificationCancel:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) handleUserVerificationRuntime(request *Request) (any, error) {
	switch request.Method {
	case MethodUserVerificationStatus:
		return userVerificationUnavailableStatus(), nil
	case MethodUserVerificationEnroll:
		var params UserVerificationEnrollParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return nil, userVerificationUnavailableError()
	case MethodUserVerificationDelete:
		var params UserVerificationDeleteParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return nil, userVerificationUnavailableError()
	case MethodUserVerificationVerify:
		var params UserVerificationVerifyParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if err := validateUserVerificationRequest(&params); err != nil {
			return nil, err
		}
		return nil, userVerificationUnavailableError()
	case MethodUserVerificationCancel:
		var params UserVerificationCancelParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		// No native work is ever started, so cancellation is a no-op ack.
		return &UserVerificationCancelResponse{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}
