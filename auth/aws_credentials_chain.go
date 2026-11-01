package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
)

// errAWSCredentialProviderNotEnabled is the aws-config identity error reported
// when no credential source in the chain is enabled. The pinned Rust SDK never
// enables the EC2 instance metadata provider, so a machine without environment
// keys or a usable profile fails with this message instead of an IMDS timeout.
var errAWSCredentialProviderNotEnabled = errors.New("the credential provider was not enabled")

// notEnabledCredentialsProvider fails every retrieve with the aws-config
// "credential provider was not enabled" identity error.
type notEnabledCredentialsProvider struct{}

func (notEnabledCredentialsProvider) Retrieve(context.Context) (awssdk.Credentials, error) {
	return awssdk.Credentials{}, errAWSCredentialProviderNotEnabled
}

// loadAWSSDKConfig mirrors Rust aws_auth::config::load_sdk_config: the SDK
// config chain with the optional profile and region overrides from
// AwsAuthConfig. IMDS credentials stay disabled, matching the pinned Rust build.
//
// The resolved profile and whether the shared config files define it are
// returned so callers can reproduce ProfileFileCredentialsProvider without a
// second lookup.
func loadAWSSDKConfig(ctx context.Context, config *AWSAuthConfig, options *AWSAuthLoadOptions) (awssdk.Config, awsconfig.SharedConfig, bool, error) {
	loadOptions := options.awsConfigOptions()
	loadOptions = append(loadOptions, awsconfig.WithEC2IMDSClientEnableState(imds.ClientDisabled))
	var profileConfig awsconfig.SharedConfig
	profileFound := false
	if profile := config.Profile; profile != "" {
		// Rust's loader tolerates an unknown profile name: it contributes no
		// region or credentials while the environment still resolves both. The
		// Go SDK fails the whole load instead, so an unknown profile is omitted.
		resolved, err := loadAWSSharedProfile(ctx, profile)
		if err == nil {
			profileConfig, profileFound = resolved, true
		}
		if profileFound || !isMissingSharedProfile(err) {
			loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(profile))
		}
	}
	if config.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(config.Region))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return awssdk.Config{}, awsconfig.SharedConfig{}, false, err
	}
	return loaded, profileConfig, profileFound, nil
}

// loadAWSSharedProfile resolves one shared config profile from the files the
// environment selects. awsconfig.LoadSharedConfigProfile defaults to
// ~/.aws/{config,credentials} and ignores AWS_CONFIG_FILE and
// AWS_SHARED_CREDENTIALS_FILE, unlike the loader behind LoadDefaultConfig.
func loadAWSSharedProfile(ctx context.Context, profile string) (awsconfig.SharedConfig, error) {
	return awsconfig.LoadSharedConfigProfile(ctx, profile, func(options *awsconfig.LoadSharedConfigOptions) {
		options.ConfigFiles = awsSharedConfigFiles("AWS_CONFIG_FILE", awsconfig.DefaultSharedConfigFiles)
		options.CredentialsFiles = awsSharedConfigFiles("AWS_SHARED_CREDENTIALS_FILE", awsconfig.DefaultSharedCredentialsFiles)
	})
}

func awsSharedConfigFiles(envKey string, defaults []string) []string {
	if path := strings.TrimSpace(os.Getenv(envKey)); path != "" {
		return []string{path}
	}
	return defaults
}

func isMissingSharedProfile(err error) bool {
	var missing awsconfig.SharedConfigProfileNotExistError
	return errors.As(err, &missing)
}

// defaultCredentialsProvider mirrors the aws-config default chain pinned by the
// Rust SDK: environment keys, the selected profile, or container credentials.
// A machine with none of them reports the "credential provider was not enabled"
// error instead of probing the instance metadata service.
func defaultCredentialsProvider(ctx context.Context, sdkChain awssdk.CredentialsProvider) awssdk.CredentialsProvider {
	if sdkChain == nil || !awsCredentialSourcesConfigured(ctx) {
		return notEnabledCredentialsProvider{}
	}
	return sdkChain
}

// profileCredentialsProvider mirrors aws-config's ProfileFileCredentialsProvider,
// which an explicit profile selects in Rust: credentials come from that profile
// only and environment keys are ignored. A profile that declares no resolvable
// source (including an unknown profile name) reports
// "the credential provider was not enabled".
func profileCredentialsProvider(profile string, resolved awsconfig.SharedConfig, found bool, sdkChain awssdk.CredentialsProvider) awssdk.CredentialsProvider {
	if !found {
		return notEnabledCredentialsProvider{}
	}
	if keys := resolved.Credentials; keys.AccessKeyID != "" && keys.SecretAccessKey != "" {
		return awscredentials.NewStaticCredentialsProvider(keys.AccessKeyID, keys.SecretAccessKey, keys.SessionToken)
	}
	if command := strings.TrimSpace(resolved.CredentialProcess); command != "" {
		return &awsCredentialProcessProvider{command: command, profile: resolved.Profile}
	}
	if sharedProfileHasCredentials(resolved) && sdkChain != nil {
		// Role assumption, SSO, and web identity keep the SDK's own providers:
		// those flows need STS/SSO clients the SDK wires up internally.
		return sdkChain
	}
	return notEnabledCredentialsProvider{}
}

// awsCredentialSourcesConfigured reports whether the SDK's default chain would
// enable a credential provider. Only explicit sources count: environment keys,
// container credentials, web identity, and the selected shared config profile.
// The EC2 instance metadata service is deliberately excluded.
func awsCredentialSourcesConfigured(ctx context.Context) bool {
	if strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "" && strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")) != "" {
		return true
	}
	for _, key := range []string{"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	if strings.TrimSpace(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) != "" && strings.TrimSpace(os.Getenv("AWS_ROLE_ARN")) != "" {
		return true
	}
	profile := firstNonEmptyAWS(os.Getenv("AWS_PROFILE"), os.Getenv("AWS_DEFAULT_PROFILE"), "default")
	resolved, err := loadAWSSharedProfile(ctx, profile)
	if err != nil {
		return false
	}
	return sharedProfileHasCredentials(resolved)
}

// sharedProfileHasCredentials reports whether a profile declares a credential
// source the SDK can resolve.
func sharedProfileHasCredentials(profile awsconfig.SharedConfig) bool {
	if profile.Credentials.AccessKeyID != "" && profile.Credentials.SecretAccessKey != "" {
		return true
	}
	for _, value := range []string{
		profile.CredentialProcess,
		profile.RoleARN,
		profile.SourceProfileName,
		profile.WebIdentityTokenFile,
		profile.SSOSessionName,
		profile.SSORegion,
		profile.SSOStartURL,
		profile.SSOAccountID,
	} {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// awsCredentialProcessProvider runs a profile's credential_process command and
// decodes its versioned JSON output, mirroring aws-config's process provider.
type awsCredentialProcessProvider struct {
	command string
	profile string
}

type awsCredentialProcessOutput struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

func (p *awsCredentialProcessProvider) Retrieve(ctx context.Context) (awssdk.Credentials, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", p.command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", p.command)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return awssdk.Credentials{}, fmt.Errorf("error in credential_process: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var output awsCredentialProcessOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return awssdk.Credentials{}, fmt.Errorf("error parsing credential_process output: %v", err)
	}
	if output.Version != 1 {
		return awssdk.Credentials{}, fmt.Errorf("credential_process returned an unsupported version: %d", output.Version)
	}
	if strings.TrimSpace(output.AccessKeyID) == "" || strings.TrimSpace(output.SecretAccessKey) == "" {
		return awssdk.Credentials{}, errors.New("credential_process returned incomplete credentials")
	}
	credentials := awssdk.Credentials{
		AccessKeyID:     output.AccessKeyID,
		SecretAccessKey: output.SecretAccessKey,
		SessionToken:    output.SessionToken,
		Source:          "CredentialProcess",
	}
	if expiration := strings.TrimSpace(output.Expiration); expiration != "" {
		expires, err := time.Parse(time.RFC3339, expiration)
		if err != nil {
			return awssdk.Credentials{}, fmt.Errorf("error parsing credential_process expiration: %v", err)
		}
		credentials.CanExpire = true
		credentials.Expires = expires.UTC()
	}
	return credentials, nil
}
