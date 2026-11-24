package appserver

import (
	"sync"
	"testing"
	"time"
)

// Rust #60046 ExecPolicyManager::apply_network_rule_and_persist holds the exec
// policy update lock across the whole amendment: publish the denial, write the
// policy file, publish the grant. A later amendment therefore cannot publish a
// decision before the earlier one is durable.
func TestNetworkPolicyAmendmentUpdateLockSerializesPublishLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once
	service.persistAmendment = func(amendment *NetworkPolicyAmendment, _ NetworkApprovalProtocol) error {
		if amendment.Host == "granted.example" {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
		}
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- service.applyCommandNetworkPolicyAmendment("thread-network", "granted.example", NetworkPolicyRuleAllow)
	}()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first amendment never reached the policy write")
	}

	// The grant is not visible while its write is still in flight.
	if allow, _ := service.hostPolicyDecision("thread-network", "granted.example"); allow {
		t.Fatal("grant published before its policy write succeeded")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- service.applyCommandNetworkPolicyAmendment("thread-network", "blocked.example", NetworkPolicyRuleDeny)
	}()
	// The denial belongs to a second amendment, so it waits for the first one's
	// write to finish and must not be visible while that write is open.
	time.Sleep(100 * time.Millisecond)
	if _, denied := service.hostPolicyDecision("thread-network", "blocked.example"); denied {
		t.Fatal("later denial published before the earlier amendment was durable")
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first amendment = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second amendment = %v", err)
	}
	if allow, _ := service.hostPolicyDecision("thread-network", "granted.example"); !allow {
		t.Fatal("grant never became visible after its write finished")
	}
	if _, denied := service.hostPolicyDecision("thread-network", "blocked.example"); !denied {
		t.Fatal("denial never became visible after the earlier write finished")
	}
}
