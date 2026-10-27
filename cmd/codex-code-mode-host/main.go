package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"codex_go/codemode"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type cliOptions struct {
	help              bool
	uiPluginProcess   bool
	listen            string
	listenSet         bool
	otelTraceListen   string
	otelTraceExporter string
	otelSet           bool
}

const usage = `Usage: codex-code-mode-host [OPTIONS]

Options:
      --codex-ui-plugin-process  Run the bounded UI plugin helper
      --listen <URL>             Transport endpoint [default: stdio]
      --otel-trace-listen <URL>  Optional OTLP trace websocket endpoint
      --otel-trace-exporter <URL> Optional OTLP/HTTP trace exporter endpoint
  -h, --help                     Print help
`

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, err := parseCLI(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if options.help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if options.uiPluginProcess {
		if err := codemode.RunUIPluginSandbox(stdin, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if err := codemode.RunStdioHost(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseCLI(args []string) (cliOptions, error) {
	options := cliOptions{listen: "stdio"}
	value := func(index *int, name string) (string, error) {
		if *index+1 >= len(args) {
			return "", fmt.Errorf("a value is required for '%s <URL>' but none was supplied", name)
		}
		*index = *index + 1
		return args[*index], nil
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--help" || argument == "-h":
			options.help = true
		case argument == "--codex-ui-plugin-process":
			options.uiPluginProcess = true
		case argument == "--listen":
			parsed, err := value(&index, "--listen")
			if err != nil {
				return cliOptions{}, err
			}
			options.listen, options.listenSet = parsed, true
		case strings.HasPrefix(argument, "--listen="):
			options.listen, options.listenSet = strings.TrimPrefix(argument, "--listen="), true
		case argument == "--otel-trace-listen":
			parsed, err := value(&index, "--otel-trace-listen")
			if err != nil {
				return cliOptions{}, err
			}
			options.otelTraceListen, options.otelSet = parsed, true
		case strings.HasPrefix(argument, "--otel-trace-listen="):
			options.otelTraceListen, options.otelSet = strings.TrimPrefix(argument, "--otel-trace-listen="), true
		case argument == "--otel-trace-exporter":
			parsed, err := value(&index, "--otel-trace-exporter")
			if err != nil {
				return cliOptions{}, err
			}
			options.otelTraceExporter, options.otelSet = parsed, true
		case strings.HasPrefix(argument, "--otel-trace-exporter="):
			options.otelTraceExporter, options.otelSet = strings.TrimPrefix(argument, "--otel-trace-exporter="), true
		default:
			return cliOptions{}, fmt.Errorf("unexpected argument '%s' found", argument)
		}
	}
	if options.help {
		return options, nil
	}
	if options.uiPluginProcess && (options.listenSet || options.otelSet) {
		return cliOptions{}, fmt.Errorf("the argument '--codex-ui-plugin-process' cannot be used with transport arguments")
	}
	return options, nil
}
