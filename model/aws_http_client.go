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
	"codex_go/network"
)

var awsApplicationHTTPClient struct {
	mu     sync.RWMutex
	client *http.Client
	policy network.NetworkPolicy
}

// SetAWSApplicationClient installs the client and policy the AWS SDK credential
// and region requests use. A nil client keeps the AWS SDK default.
func SetAWSApplicationClient(client *http.Client, policy network.NetworkPolicy) {
	awsApplicationHTTPClient.mu.Lock()
	defer awsApplicationHTTPClient.mu.Unlock()
	awsApplicationHTTPClient.client = client
	awsApplicationHTTPClient.policy = policy
}

// SetAWSHTTPClient installs only the client, leaving the policy unmanaged.
func SetAWSHTTPClient(client *http.Client) {
	SetAWSApplicationClient(client, network.UnmanagedNetworkPolicy())
}

// AWSHTTPClient returns the installed client, or nil when the host installed
// none.
func AWSHTTPClient() *http.Client {
	awsApplicationHTTPClient.mu.RLock()
	defer awsApplicationHTTPClient.mu.RUnlock()
	return awsApplicationHTTPClient.client
}

// AWSNetworkPolicy returns the installed application policy, or an unmanaged
// policy when the host installed none.
func AWSNetworkPolicy() network.NetworkPolicy {
	awsApplicationHTTPClient.mu.RLock()
	defer awsApplicationHTTPClient.mu.RUnlock()
	return awsApplicationHTTPClient.policy
}

// awsAuthLoadOptions carries the installed client into the AWS config chain.
func awsAuthLoadOptions() *auth.AWSAuthLoadOptions {
	client := AWSHTTPClient()
	policy := AWSNetworkPolicy()
	if client == nil && !policy.IsScoped() {
		return nil
	}
	return &auth.AWSAuthLoadOptions{HTTPClient: client, Policy: policy}
}
