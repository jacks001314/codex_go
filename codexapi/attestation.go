package codexapi

import (
	"context"
	"strings"
	"sync"
)

const AttestationHeader = "x-oai-attestation"

type AttestationContext struct {
	ThreadID string
}

type AttestationProvider interface {
	HeaderForRequest(ctx context.Context, request *AttestationContext) (string, bool, error)
}

type StaticAttestationProvider struct {
	value string
}

func NewStaticAttestationProvider(value string) *StaticAttestationProvider {
	return &StaticAttestationProvider{value: value}
}

func (p *StaticAttestationProvider) HeaderForRequest(ctx context.Context, request *AttestationContext) (string, bool, error) {
	_ = ctx
	_ = request
	value := strings.TrimSpace(p.value)
	return value, value != "", nil
}

type CountingAttestationProvider struct {
	mu    sync.Mutex
	value string
	calls int
}

func NewCountingAttestationProvider(value string) *CountingAttestationProvider {
	return &CountingAttestationProvider{value: value}
}

func (p *CountingAttestationProvider) HeaderForRequest(ctx context.Context, request *AttestationContext) (string, bool, error) {
	_ = ctx
	_ = request
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	value := strings.TrimSpace(p.value)
	return value, value != "", nil
}

func (p *CountingAttestationProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type AttestationGenerator struct {
	include  bool
	provider AttestationProvider
}

func NewAttestationGenerator(include bool, provider AttestationProvider) *AttestationGenerator {
	return &AttestationGenerator{include: include, provider: provider}
}

func (g *AttestationGenerator) Header(ctx context.Context, request *AttestationContext) (string, bool, error) {
	if g == nil || !g.include || g.provider == nil {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return g.provider.HeaderForRequest(ctx, request)
}

func AddAttestationHeader(headers map[string]string, value string) map[string]string {
	value = strings.TrimSpace(value)
	if value == "" {
		return headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers[AttestationHeader] = value
	return headers
}
