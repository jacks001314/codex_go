package appserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codex_go/internal/session"
)

var ErrUnixSocketUnsupported = errors.New("app-server unix socket transport is not supported on this platform")

type UnixSocketOptions struct {
	CodexHome      string
	StoreRoot      string
	Listen         string
	RuntimeOptions *RuntimeRouterOptions
}

func UnixSocketPath(listen string, codexHome string) (string, error) {
	listen = strings.TrimSpace(listen)
	if listen == "" || listen == "unix://" {
		return AppServerControlSocketPath(codexHome), nil
	}
	if strings.HasPrefix(listen, "unix://") {
		rest := strings.TrimPrefix(listen, "unix://")
		if rest == "" {
			return AppServerControlSocketPath(codexHome), nil
		}
		if strings.HasPrefix(rest, "/") {
			return filepath.Clean(rest), nil
		}
		parsed, err := url.Parse(listen)
		if err == nil && parsed.Host != "" {
			if parsed.Path != "" {
				return absoluteUnixSocketPath(parsed.Host + parsed.Path)
			}
			return absoluteUnixSocketPath(parsed.Host)
		}
		return absoluteUnixSocketPath(rest)
	}
	return "", fmt.Errorf("unsupported app-server unix listen address %s", listen)
}

func absoluteUnixSocketPath(path string) (string, error) {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func NewUnixSocketRouter(codexHome string) *RuntimeRouter {
	return NewUnixSocketRouterWithOptions(codexHome, nil)
}

func NewUnixSocketRouterWithOptions(codexHome string, options *RuntimeRouterOptions) *RuntimeRouter {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		codexHome = ".codex"
	}
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	return NewDefaultRuntimeRouterWithOptions(store, codexHome, options)
}

func ServeUnixSocket(ctx context.Context, options *UnixSocketOptions) error {
	if options == nil {
		options = &UnixSocketOptions{}
	}
	codexHome := strings.TrimSpace(options.CodexHome)
	if codexHome == "" {
		codexHome = ".codex"
	}
	socketPath, err := UnixSocketPath(options.Listen, codexHome)
	if err != nil {
		return err
	}
	return serveUnixSocket(ctx, socketPath, func() *RuntimeRouter {
		if strings.TrimSpace(options.StoreRoot) != "" {
			return NewDefaultRuntimeRouterWithOptions(session.NewStore(options.StoreRoot), codexHome, options.RuntimeOptions)
		}
		return NewUnixSocketRouterWithOptions(codexHome, options.RuntimeOptions)
	})
}

func ensureUnixSocketParent(socketPath string) error {
	dir := filepath.Dir(socketPath)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func serveUnixSocketConnection(conn net.Conn, router *RuntimeRouter) error {
	defer conn.Close()
	if router != nil {
		defer router.Close()
	}
	return serveJSONLineConnection(router, conn, conn)
}
