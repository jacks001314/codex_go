package model

// Process-scoped client for AWS credential and region requests.
//
// Rust parity: codex-rs/aws-auth (#47408) routes the AWS SDK's credential and
// region HTTP through the config's application client, so a managed application
// network policy checks the destination before the request leaves the process.
// AWS credential discovery is itself process-scoped (environment variables, the
// shared config chain, the instance metadata service), so Go's hosts install one
// client for the process rather than threading it through every provider.

import (
	"net/http"
	"sync"

	"codex_go/auth"
)

var awsApplicationHTTPClient struct {
	mu     sync.RWMutex
	client *http.Client
}

// SetAWSHTTPClient installs the client the AWS SDK credential and region
// requests use. A nil client keeps the AWS SDK default.
func SetAWSHTTPClient(client *http.Client) {
	awsApplicationHTTPClient.mu.Lock()
	defer awsApplicationHTTPClient.mu.Unlock()
	awsApplicationHTTPClient.client = client
}

// AWSHTTPClient returns the installed client, or nil when the host installed
// none.
func AWSHTTPClient() *http.Client {
	awsApplicationHTTPClient.mu.RLock()
	defer awsApplicationHTTPClient.mu.RUnlock()
	return awsApplicationHTTPClient.client
}

// awsAuthLoadOptions carries the installed client into the AWS config chain.
func awsAuthLoadOptions() *auth.AWSAuthLoadOptions {
	client := AWSHTTPClient()
	if client == nil {
		return nil
	}
	return &auth.AWSAuthLoadOptions{HTTPClient: client}
}
