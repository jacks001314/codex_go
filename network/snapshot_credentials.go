package network

import "strings"

// CredentialMaskPair maps one real child credential to its brokered dummy.
type CredentialMaskPair struct {
	Real  string
	Dummy string
}

// SnapshotSourceEnv returns the proxy's pre-virtualization environment so the
// executor can capture the user's trusted startup state with real credentials.
func (p *PreparedProxyManagedNetwork) SnapshotSourceEnv() map[string]string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneProxyEnv(p.baseEnv)
}

// PrepareSnapshotCredentialMask binds destination hints and registers dummies
// for a real credential environment. The returned pairs are safe for use only
// inside the executor process.
func (p *PreparedProxyManagedNetwork) PrepareSnapshotCredentialMask(env map[string]string) []CredentialMaskPair {
	if p == nil || p.server == nil {
		return nil
	}
	broker := p.server.runtimePolicy().broker
	if broker == nil || !broker.Enabled() {
		return nil
	}
	broker.SetDestinationHints(env)
	virtualized := cloneProxyEnv(env)
	broker.VirtualizeChildEnv(virtualized)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	pairs := make([]CredentialMaskPair, 0, len(broker.credentials))
	seen := map[string]struct{}{}
	for _, credential := range broker.credentials {
		if credential.RealValue == "" || credential.DummyValue == "" || credential.RealValue == credential.DummyValue {
			continue
		}
		if _, duplicate := seen[credential.RealValue]; duplicate {
			continue
		}
		seen[credential.RealValue] = struct{}{}
		pairs = append(pairs, CredentialMaskPair{Real: credential.RealValue, Dummy: credential.DummyValue})
	}
	return pairs
}

// MaskSnapshotText replaces real credential values with their brokered dummies
// before snapshot state leaves executor-local capture.
func MaskSnapshotText(text string, pairs []CredentialMaskPair) string {
	for _, pair := range pairs {
		if pair.Real == "" || pair.Dummy == "" || pair.Real == pair.Dummy {
			continue
		}
		text = strings.ReplaceAll(text, pair.Real, pair.Dummy)
	}
	return text
}

// RestoreSnapshotCredentials restores a launch environment when masking cannot
// safely be replayed and disables the broker marker for that process.
func RestoreSnapshotCredentials(env map[string]string, pairs []CredentialMaskPair) {
	if env == nil {
		return
	}
	for _, pair := range pairs {
		if pair.Real == "" || pair.Dummy == "" {
			continue
		}
		for key, value := range env {
			if value == pair.Dummy {
				env[key] = pair.Real
			} else if strings.Contains(value, pair.Dummy) {
				env[key] = strings.ReplaceAll(value, pair.Dummy, pair.Real)
			}
		}
	}
	delete(env, CredentialBrokerActiveEnvKey)
	delete(env, BrokeredCredentialsEnvKey)
}

// HandleUnmaskedCredentials checks a prepared child environment for real
// credentials the broker could not virtualize.
func (p *PreparedProxyManagedNetwork) HandleUnmaskedCredentials(env map[string]string, mode CredentialMaskingFailureMode) error {
	if p == nil || p.server == nil {
		return nil
	}
	return p.server.runtimePolicy().broker.HandleUnmaskedCredentials(env, mode)
}

// RestoreUnbrokeredEnvironment restores the executor's pre-virtualization
// environment and removes broker activation for this child only. Existing
// placeholders issued to earlier commands remain valid on the proxy.
func (p *PreparedProxyManagedNetwork) RestoreUnbrokeredEnvironment(env map[string]string) {
	if p == nil || p.server == nil || env == nil {
		return
	}
	p.server.runtimePolicy().broker.RestoreAndDisableChildEnv(env)
}
