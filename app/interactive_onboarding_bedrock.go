package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"codex_go/appserver"
	"codex_go/auth"
)

func interactiveBedrockCallbacks(codexHome string) (
	func(context.Context) (*appserver.BedrockDiscoverResponse, error),
	func(context.Context, appserver.BedrockSetupParams) error,
	func(context.Context, string, string) error,
	func(context.Context, auth.LoginAccountParams) error,
	func(context.Context) (bool, error),
) {
	call := func(ctx context.Context, method appserver.Method, params any) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		router := appserver.NewDefaultRuntimeRouter(newSessionStore(), codexHome)
		defer router.Close()
		response := router.Handle(&appserver.Request{
			ID:     appserver.IntID(1),
			Method: method,
			Params: raw,
		})
		if response == nil {
			return nil, errors.New("app server returned no response")
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	}
	discover := func(ctx context.Context) (*appserver.BedrockDiscoverResponse, error) {
		result, err := call(ctx, appserver.MethodBedrockDiscover, appserver.BedrockDiscoverParams{})
		if err != nil {
			return nil, err
		}
		response, ok := result.(*appserver.BedrockDiscoverResponse)
		if !ok || response == nil {
			return nil, fmt.Errorf("unexpected Bedrock discover response %T", result)
		}
		return response, nil
	}
	setup := func(ctx context.Context, params appserver.BedrockSetupParams) error {
		_, err := call(ctx, appserver.MethodBedrockSetup, params)
		return err
	}
	loginAPIKey := func(ctx context.Context, apiKey string, region string) error {
		_, err := call(ctx, appserver.MethodLoginAccount, auth.LoginAccountParams{
			Type:   auth.AccountAmazonBedrock,
			APIKey: apiKey,
			Region: region,
		})
		return err
	}
	loginAccessKeys := func(ctx context.Context, params auth.LoginAccountParams) error {
		_, err := call(ctx, appserver.MethodLoginAccount, params)
		return err
	}
	checkGovCloud := func(ctx context.Context) (bool, error) {
		result, err := call(ctx, appserver.MethodBedrockCheckGovCloudRequirements, appserver.BedrockCheckGovCloudRequirementsParams{})
		if err != nil {
			return false, err
		}
		response, ok := result.(*appserver.BedrockCheckGovCloudRequirementsResponse)
		if !ok || response == nil {
			return false, fmt.Errorf("unexpected Bedrock GovCloud response %T", result)
		}
		return response.IsGovCloud, nil
	}
	return discover, setup, loginAPIKey, loginAccessKeys, checkGovCloud
}
