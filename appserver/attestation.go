package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"codex_go/codexapi"
)

const appServerAttestationGenerateTimeout = 100 * time.Millisecond

type appServerAttestationStatus uint8

const (
	appServerAttestationStatusOK appServerAttestationStatus = iota
	appServerAttestationStatusTimeout
	appServerAttestationStatusRequestFailed
	appServerAttestationStatusRequestCanceled
	appServerAttestationStatusMalformedResponse
)

type appServerAttestationProvider struct {
	router *RuntimeRouter
}

func (r *RuntimeRouter) appServerAttestationProvider() codexapi.AttestationProvider {
	if r == nil {
		return nil
	}
	return &appServerAttestationProvider{router: r}
}

func (p *appServerAttestationProvider) HeaderForRequest(ctx context.Context, request *codexapi.AttestationContext) (string, bool, error) {
	if p == nil || p.router == nil || request == nil {
		return "", false, nil
	}
	threadID := strings.TrimSpace(request.ThreadID)
	if threadID == "" {
		return "", false, nil
	}
	connectionID, ok := p.router.firstAttestationCapableConnectionForThread(threadID)
	if !ok {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, appServerAttestationGenerateTimeout)
	defer cancel()
	var response AttestationGenerateResponse
	err := p.router.requireServerRequests().RequestToConnection(requestCtx, connectionID, ServerRequestAttestationGenerate, AttestationGenerateParams{}, &response)
	if err != nil {
		status := appServerAttestationStatusForError(err)
		value, marshalErr := appServerAttestationHeaderValue(status, nil)
		return value, marshalErr == nil && strings.TrimSpace(value) != "", marshalErr
	}
	token := response.Token
	value, err := appServerAttestationHeaderValue(appServerAttestationStatusOK, &token)
	return value, err == nil && strings.TrimSpace(value) != "", err
}

func appServerAttestationStatusForError(err error) appServerAttestationStatus {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return appServerAttestationStatusTimeout
	case errors.Is(err, context.Canceled):
		return appServerAttestationStatusRequestCanceled
	case isAppServerAttestationMalformedResponse(err):
		return appServerAttestationStatusMalformedResponse
	default:
		return appServerAttestationStatusRequestFailed
	}
}

func isAppServerAttestationMalformedResponse(err error) bool {
	if err == nil {
		return false
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return true
	}
	return strings.HasPrefix(err.Error(), "json: cannot unmarshal")
}

func appServerAttestationHeaderValue(status appServerAttestationStatus, token *string) (string, error) {
	data, err := json.Marshal(struct {
		V uint8   `json:"v"`
		S uint8   `json:"s"`
		T *string `json:"t,omitempty"`
	}{
		V: 1,
		S: uint8(status),
		T: token,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}
