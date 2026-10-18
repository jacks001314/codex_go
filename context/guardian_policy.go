package context

import "strings"

// GuardianPolicy is the isolated developer policy supplied to a Guardian
// reviewer, mirroring Rust context::GuardianPolicy (#40420).
type GuardianPolicy struct {
	Policy string
}

func NewGuardianPolicy(policy string) *GuardianPolicy {
	if strings.TrimSpace(policy) == "" {
		return nil
	}
	return &GuardianPolicy{Policy: policy}
}

func (p *GuardianPolicy) Role() string {
	return RoleDeveloper
}

// Guardian policy is unmarked and must be sent as its own developer message.
func (p *GuardianPolicy) Markers() (string, string) {
	return "", ""
}

func (p *GuardianPolicy) Body() string {
	if p == nil {
		return ""
	}
	return p.Policy
}

func (p *GuardianPolicy) ContentKind() string {
	return "guardian.policy"
}
