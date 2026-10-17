package app

import (
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/mcp"
)

// UserVerificationFailure keeps the safe display message and closed native
// error category separate from proof material.
type UserVerificationFailure struct {
	Message string
	Details appserver.UserVerificationErrorDetails
}

func verificationFailure(err error) UserVerificationFailure {
	failure := UserVerificationFailure{
		Message: "Could not complete user verification with the local Codex binary.",
		Details: appserver.UserVerificationErrorDetails{
			Type:   "failed",
			Reason: string(appserver.UserVerificationFailureServiceError),
		},
	}
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) || rpcErr == nil || rpcErr.Data == nil {
		return failure
	}
	details := appserver.UserVerificationErrorDetails{
		Type:   strings.TrimSpace(remoteTUIAnyString(rpcErr.Data["type"])),
		Reason: strings.TrimSpace(remoteTUIAnyString(rpcErr.Data["reason"])),
	}
	if recognizedUserVerificationDetails(details) {
		failure.Details = details
	}
	return failure
}

func userVerificationFailureReason(failure UserVerificationFailure) mcp.UserVerificationReason {
	switch failure.Details.Type {
	case "invalidRequest":
		if failure.Details.Reason == string(appserver.UserVerificationInvalidRequestInvalidParams) {
			return mcp.UserVerificationReasonInvalidParams
		}
	case "unavailable":
		switch appserver.UserVerificationUnavailableReason(failure.Details.Reason) {
		case appserver.UserVerificationUnavailableCredentialMissing:
			return mcp.UserVerificationReasonCredentialMissing
		case appserver.UserVerificationUnavailableBiometricsUnavailable:
			return mcp.UserVerificationReasonBiometricsUnavailable
		case appserver.UserVerificationUnavailableProviderUnavailable:
			return mcp.UserVerificationReasonProviderUnavailable
		}
	case "cancelled":
		switch appserver.UserVerificationCancellationReason(failure.Details.Reason) {
		case appserver.UserVerificationCancellationUserCancelled:
			return mcp.UserVerificationReasonUserCancelled
		case appserver.UserVerificationCancellationInterrupted:
			return mcp.UserVerificationReasonInterrupted
		}
	case "failed":
		switch appserver.UserVerificationFailureReason(failure.Details.Reason) {
		case appserver.UserVerificationFailureAuthenticationFailed:
			return mcp.UserVerificationReasonAuthenticationFailed
		case appserver.UserVerificationFailureTimeout:
			return mcp.UserVerificationReasonTimeout
		case appserver.UserVerificationFailureProviderError:
			return mcp.UserVerificationReasonProviderError
		case appserver.UserVerificationFailureServiceError:
			return mcp.UserVerificationReasonServiceError
		}
	}
	return mcp.UserVerificationReasonServiceError
}

func recognizedUserVerificationDetails(details appserver.UserVerificationErrorDetails) bool {
	switch details.Type {
	case "invalidRequest":
		return details.Reason == string(appserver.UserVerificationInvalidRequestInvalidParams)
	case "unavailable":
		switch appserver.UserVerificationUnavailableReason(details.Reason) {
		case appserver.UserVerificationUnavailableCredentialMissing,
			appserver.UserVerificationUnavailableBiometricsUnavailable,
			appserver.UserVerificationUnavailableProviderUnavailable:
			return true
		}
	case "cancelled":
		switch appserver.UserVerificationCancellationReason(details.Reason) {
		case appserver.UserVerificationCancellationUserCancelled,
			appserver.UserVerificationCancellationInterrupted:
			return true
		}
	case "failed":
		switch appserver.UserVerificationFailureReason(details.Reason) {
		case appserver.UserVerificationFailureAuthenticationFailed,
			appserver.UserVerificationFailureTimeout,
			appserver.UserVerificationFailureProviderError,
			appserver.UserVerificationFailureServiceError:
			return true
		}
	}
	return false
}
