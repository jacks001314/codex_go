package app

import (
	"context"
	"encoding/json"
	"errors"

	"codex_go/appserver"
)

// interactiveGcpCallbacks returns the Google Cloud Vertex AI setup callback the
// onboarding wizard uses. Setup runs against a short-lived embedded router,
// matching the Bedrock wizard so the local app-server persists the provider
// configuration (Rust #57857).
func interactiveGcpCallbacks(codexHome string) func(context.Context, appserver.GcpSetupParams) error {
	return func(ctx context.Context, params appserver.GcpSetupParams) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), codexHome)
		defer router.Close()
		response := router.Handle(&appserver.Request{
			ID:     appserver.IntID(1),
			Method: appserver.MethodGcpSetup,
			Params: raw,
		})
		if response == nil {
			return errors.New("app server returned no response")
		}
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return nil
	}
}
