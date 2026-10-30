package appserver

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingUserVerificationProvider blocks Status until the test releases it,
// signalling when the worker has started.
type blockingUserVerificationProvider struct {
	UnsupportedUserVerificationProvider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingUserVerificationProvider) Status() (*UserVerificationStatusResponse, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return userVerificationUnavailableStatus(), nil
}

// workerPermit is a minimal stand-in for Rust #43568's one-permit semaphore.
type workerPermit struct {
	mu   sync.Mutex
	busy bool
}

func (p *workerPermit) acquire() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		return false
	}
	p.busy = true
	return true
}

func (p *workerPermit) release() {
	p.mu.Lock()
	p.busy = false
	p.mu.Unlock()
}

func statusRequest() *Request {
	return &Request{Method: MethodUserVerificationStatus, Params: []byte("{}")}
}

func userVerificationFailureReason(t *testing.T, err error) string {
	t.Helper()
	var verificationErr *userVerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("error = %v, want a user verification error", err)
	}
	return verificationErr.data.Reason
}

// TestUserVerificationWorkerRejectsConcurrentOperationLikeRust mirrors Rust
// #43568: one native operation per service, and a concurrent request is
// rejected with a provider error instead of queueing.
func TestUserVerificationWorkerRejectsConcurrentOperationLikeRust(t *testing.T) {
	provider := &blockingUserVerificationProvider{started: make(chan struct{}), release: make(chan struct{})}
	permit := &workerPermit{}
	service := &userVerificationService{
		provider: provider,
		acquire:  permit.acquire,
		release:  permit.release,
		timeout:  time.Second,
	}
	first := make(chan struct{})
	go func() {
		defer close(first)
		if _, err := service.handle(statusRequest(), "stdio"); err != nil {
			t.Errorf("first status error = %v", err)
		}
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first status never reached the provider")
	}
	if _, err := service.handle(statusRequest(), "stdio"); userVerificationFailureReason(t, err) != string(UserVerificationFailureProviderError) {
		t.Fatalf("concurrent status error = %v, want provider error", err)
	}
	close(provider.release)
	<-first
	if _, err := service.handle(statusRequest(), "stdio"); err != nil {
		t.Fatalf("status after the worker finished error = %v", err)
	}
}

// TestUserVerificationWorkerTimesOutLikeRust mirrors Rust #43568's bounded
// worker: a slow provider reports the timeout reason and keeps the slot taken
// until the provider actually returns.
func TestUserVerificationWorkerTimesOutLikeRust(t *testing.T) {
	provider := &blockingUserVerificationProvider{started: make(chan struct{}), release: make(chan struct{})}
	permit := &workerPermit{}
	service := &userVerificationService{
		provider: provider,
		acquire:  permit.acquire,
		release:  permit.release,
		timeout:  20 * time.Millisecond,
	}
	if _, err := service.handle(statusRequest(), "stdio"); userVerificationFailureReason(t, err) != string(UserVerificationFailureTimeout) {
		t.Fatalf("slow status error = %v, want timeout", err)
	}
	if _, err := service.handle(statusRequest(), "stdio"); userVerificationFailureReason(t, err) != string(UserVerificationFailureProviderError) {
		t.Fatalf("status during a timed-out worker error = %v, want provider error", err)
	}
	close(provider.release)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := service.handle(statusRequest(), "stdio"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("worker slot was not released after the provider returned")
		}
		time.Sleep(time.Millisecond)
	}
}
