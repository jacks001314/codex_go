// Command codex-network-proxy runs the Codex network policy proxy from a
// standalone JSON configuration, without a full Codex permissions profile
// (Rust codex-network-proxy, #46573).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) > 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if trimmed := *configPath; trimmed == "" {
		return errors.New("network proxy requires --config <PATH>")
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("failed to read network proxy config: %w", err)
	}
	config, err := network.ParseStandaloneProxyConfig(data)
	if err != nil {
		return fmt.Errorf("invalid network proxy config: %w", err)
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
