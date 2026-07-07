package telemetry

import (
	"context"
	"os"
	"strings"
)

const (
	DefaultAnalyticsEnabled = false
	DefaultLogFilter        = "error,opentelemetry_sdk=off,opentelemetry_otlp=off"
	ExecServerServiceName   = "codex-exec-server"
)

type Config struct {
	ServiceName      string
	AnalyticsEnabled bool
	LogFilter        string
}

func DefaultConfig() Config {
	return Config{ServiceName: ExecServerServiceName, AnalyticsEnabled: DefaultAnalyticsEnabled, LogFilter: EnvFilter()}
}

func EnvFilter() string {
	value := strings.TrimSpace(os.Getenv("RUST_LOG"))
	if value == "" {
		value = strings.TrimSpace(os.Getenv("CODEX_LOG"))
	}
	if value == "" {
		return DefaultLogFilter
	}
	return value
}

func RunUntilShutdown(ctx context.Context, run func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if run == nil {
		return nil
	}
	return run(ctx)
}
