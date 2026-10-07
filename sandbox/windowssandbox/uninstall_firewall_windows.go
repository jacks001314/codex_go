//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"strings"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// legacyFirewallRuleNames are the internal names the sandbox setup adds to the
// machine-wide firewall policy. The list matches Rust
// uninstall_windows::firewall::cleanup_firewall_rules (#50437); the inbound rule
// is reserved even though this port's setup helper does not create it.
var legacyFirewallRuleNames = []string{
	"codex_sandbox_offline_block_outbound",
	"codex_sandbox_offline_block_inbound",
	"codex_sandbox_offline_block_loopback_tcp",
	"codex_sandbox_offline_block_loopback_udp",
	"codex_sandbox_offline_allow_loopback_proxy",
}

// removeLegacyFirewallRules deletes the sandbox firewall rules by their internal
// names, mirroring Rust firewall::cleanup_firewall_rules.
func removeLegacyFirewallRules() error {
	// COM automation is thread-scoped; pin the goroutine for the whole call.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return fmt.Errorf("access firewall policy for sandbox uninstall: %w", err)
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("HNetCfg.FwPolicy2")
	if err != nil {
		return fmt.Errorf("access firewall policy for sandbox uninstall: %w", err)
	}
	defer unknown.Release()
	policy, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("access firewall policy for sandbox uninstall: %w", err)
	}
	defer policy.Release()

	rulesValue, err := oleutil.GetProperty(policy, "Rules")
	if err != nil {
		return fmt.Errorf("access sandbox firewall rules: %w", err)
	}
	defer rulesValue.Clear()
	rules := rulesValue.ToIDispatch()
	if rules == nil {
		return fmt.Errorf("access sandbox firewall rules: INetFwPolicy2::Rules returned a non-dispatch value")
	}
	defer rules.Release()

	var failures []string
	for _, name := range legacyFirewallRuleNames {
		value, err := oleutil.CallMethod(rules, "Remove", name)
		if value != nil {
			_ = value.Clear()
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("remove sandbox firewall rule %s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}
