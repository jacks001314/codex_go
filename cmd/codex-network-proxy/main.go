// Command codex-network-proxy runs the Codex network policy proxy from a
// standalone JSON configuration, without a full Codex permissions profile
// (Rust codex-network-proxy, #46573).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"codex_go/network"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("codex-network-proxy", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "standalone JSON configuration containing a `network` object")
	strict := flags.Bool("strict", false, "require explicit constraints and reject unknown fields at every level")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) > 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if trimmed := *configPath; trimmed == "" {
		return errors.New("network proxy requires --config <PATH>")
	}
	data, err := readBoundedConfigFile(*configPath, network.MaxStandaloneProxyConfigBytes)
	if err != nil {
		return fmt.Errorf("failed to read network proxy config: %w", err)
	}
	var config *network.ProxyConfig
	if *strict {
		static, err := network.FromStaticProxyConfigBytesStrict(data)
		if err != nil {
			return fmt.Errorf("invalid network proxy config: %w", err)
		}
		if err := static.Validate(); err != nil {
			return fmt.Errorf("invalid network proxy config: %w", err)
		}
		config = &network.ProxyConfig{Network: static.Network}
	} else {
		parsed, err := network.ParseStandaloneProxyConfig(data)
		if err != nil {
			return fmt.Errorf("invalid network proxy config: %w", err)
		}
		config = parsed
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := network.StartStandaloneProxy(ctx, *config, processEnvironment())
	if err != nil {
		return fmt.Errorf("failed to start network proxy: %w", err)
	}
	defer server.Close()
	return server.Wait()
}

// readBoundedConfigFile reads at most limit+1 bytes so an oversized document is
// rejected without buffering the whole file (Rust #57180).
func readBoundedConfigFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("network proxy config exceeds %d bytes", limit)
	}
	return data, nil
}

func processEnvironment() map[string]string {
	environ := os.Environ()
	values := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, found := cutEnvEntry(entry)
		if !found {
			continue
		}
		values[key] = value
	}
	return values
}

func cutEnvEntry(entry string) (string, string, bool) {
	for index := 0; index < len(entry); index++ {
		if entry[index] == '=' {
			return entry[:index], entry[index+1:], true
		}
	}
	return "", "", false
}
