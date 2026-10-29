//go:build windows

package network

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alexbrainman/sspi"
	"github.com/alexbrainman/sspi/negotiate"
	"github.com/alexbrainman/sspi/ntlm"
)

// proxyAuthSupported reports whether the signed-in Windows user can answer a
// Negotiate/NTLM proxy challenge through SSPI.
func proxyAuthSupported() bool { return true }

type sspiProxyAuthSession struct {
	authScheme  string
	credentials *sspi.Credentials
	negotiate   *negotiate.ClientContext
	ntlm        *ntlm.ClientContext
	initial     []byte
}

func newProxyAuthSession(scheme string, proxyHost string) (proxyAuthSession, error) {
	scheme = canonicalAuthScheme(scheme)
	switch scheme {
	case "Negotiate":
		credentials, err := negotiate.AcquireCurrentUserCredentials()
		if err != nil {
			return nil, fmt.Errorf("acquire Windows Negotiate credentials: %w", err)
		}
		context, token, err := negotiate.NewClientContext(credentials, "HTTP/"+proxyHost)
		if err != nil {
			_ = credentials.Release()
			return nil, fmt.Errorf("start Windows Negotiate context: %w", err)
		}
		return &sspiProxyAuthSession{authScheme: scheme, credentials: credentials, negotiate: context, initial: token}, nil
	case "NTLM":
		credentials, err := ntlm.AcquireCurrentUserCredentials()
		if err != nil {
			return nil, fmt.Errorf("acquire Windows NTLM credentials: %w", err)
		}
		context, token, err := ntlm.NewClientContext(credentials)
		if err != nil {
			_ = credentials.Release()
			return nil, fmt.Errorf("start Windows NTLM context: %w", err)
		}
		return &sspiProxyAuthSession{authScheme: scheme, credentials: credentials, ntlm: context, initial: token}, nil
	default:
		return nil, errors.New("unsupported proxy authentication scheme: " + strings.TrimSpace(scheme))
	}
}

func (s *sspiProxyAuthSession) scheme() string { return s.authScheme }

func (s *sspiProxyAuthSession) initialToken() []byte { return s.initial }

func (s *sspiProxyAuthSession) update(challenge []byte) ([]byte, error) {
	if s.negotiate != nil {
		_, token, err := s.negotiate.Update(challenge)
		return token, err
	}
	if s.ntlm != nil {
		return s.ntlm.Update(challenge)
	}
	return nil, errors.New("proxy authentication session is not initialized")
}

func (s *sspiProxyAuthSession) release() {
	if s == nil {
		return
	}
	if s.negotiate != nil {
		_ = s.negotiate.Release()
		s.negotiate = nil
	}
	if s.ntlm != nil {
		_ = s.ntlm.Release()
		s.ntlm = nil
	}
	if s.credentials != nil {
		_ = s.credentials.Release()
		s.credentials = nil
	}
}
