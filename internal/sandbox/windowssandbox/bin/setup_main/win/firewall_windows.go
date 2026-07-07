//go:build windows

package win

import (
	"fmt"
	"io"
	"strings"

	"codex_go/internal/sandbox/windowssandbox"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const (
	netFwRuleDirOut    = 2
	netFwActionBlock   = 0
	netFwProfile2All   = 0x7fffffff
	netFwModifyStateOK = 0
)

func EnsureOfflineProxyAllowlist(offlineSID string, proxyPorts []uint16, allowLocalBinding bool, log io.Writer) error {
	offlineSID = strings.TrimSpace(offlineSID)
	if offlineSID == "" {
		return windowssandbox.ErrInvalidRequest
	}
	localUserSpec := firewallLocalUserSpec(offlineSID)
	return withFirewallRules(func(policy *ole.IDispatch, rules *ole.IDispatch) error {
		if allowLocalBinding {
			if err := removeFirewallRuleIfPresent(rules, offlineProxyAllowRuleName, log); err != nil {
				return err
			}
			if err := removeFirewallRuleIfPresent(rules, offlineBlockLoopbackUDPRuleName, log); err != nil {
				return err
			}
			return removeFirewallRuleIfPresent(rules, offlineBlockLoopbackTCPRuleName, log)
		}
		if err := ensureFirewallBlockRule(rules, firewallBlockRuleSpec{
			internalName:       offlineBlockLoopbackUDPRuleName,
			friendlyDesc:       offlineBlockLoopbackUDPRuleFriendly,
			protocol:           netFwIPProtocolUDP,
			localUserSpec:      localUserSpec,
			offlineSID:         offlineSID,
			remoteAddresses:    loopbackRemoteAddresses,
			remoteAddressIsSet: true,
		}, log); err != nil {
			return err
		}
		if err := ensureFirewallBlockRule(rules, firewallBlockRuleSpec{
			internalName:       offlineBlockLoopbackTCPRuleName,
			friendlyDesc:       offlineBlockLoopbackTCPRuleFriendly,
			protocol:           netFwIPProtocolTCP,
			localUserSpec:      localUserSpec,
			offlineSID:         offlineSID,
			remoteAddresses:    loopbackRemoteAddresses,
			remoteAddressIsSet: true,
		}, log); err != nil {
			return err
		}
		if err := removeFirewallRuleIfPresent(rules, offlineProxyAllowRuleName, log); err != nil {
			return err
		}
		if remotePorts, ok := blockedLoopbackTCPRemotePorts(proxyPorts); ok {
			return ensureFirewallBlockRule(rules, firewallBlockRuleSpec{
				internalName:       offlineBlockLoopbackTCPRuleName,
				friendlyDesc:       offlineBlockLoopbackTCPRuleFriendly,
				protocol:           netFwIPProtocolTCP,
				localUserSpec:      localUserSpec,
				offlineSID:         offlineSID,
				remoteAddresses:    loopbackRemoteAddresses,
				remoteAddressIsSet: true,
				remotePorts:        remotePorts,
				remotePortsIsSet:   true,
			}, log)
		}
		return nil
	})
}

func EnsureOfflineOutboundBlock(offlineSID string, log io.Writer) error {
	offlineSID = strings.TrimSpace(offlineSID)
	if offlineSID == "" {
		return windowssandbox.ErrInvalidRequest
	}
	localUserSpec := firewallLocalUserSpec(offlineSID)
	return withFirewallRules(func(policy *ole.IDispatch, rules *ole.IDispatch) error {
		return ensureFirewallBlockRule(rules, firewallBlockRuleSpec{
			internalName:       offlineBlockRuleName,
			friendlyDesc:       offlineBlockRuleFriendly,
			protocol:           netFwIPProtocolAny,
			localUserSpec:      localUserSpec,
			offlineSID:         offlineSID,
			remoteAddresses:    nonLoopbackRemoteAddresses,
			remoteAddressIsSet: true,
		}, log)
	})
}

func withFirewallRules(fn func(policy *ole.IDispatch, rules *ole.IDispatch) error) error {
	if fn == nil {
		return windowssandbox.ErrInvalidRequest
	}
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallCOMInitFailed,
			fmt.Sprintf("CoInitializeEx failed: %v", err),
		)
	}
	defer ole.CoUninitialize()

	policy, err := createDispatchObject("HNetCfg.FwPolicy2")
	if err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallPolicyAccessFailed,
			fmt.Sprintf("CoCreateInstance HNetCfg.FwPolicy2 failed: %v", err),
		)
	}
	defer policy.Release()

	if err := ensureLocalPolicyRulesTakeEffect(policy); err != nil {
		return err
	}
	rulesValue, err := oleutil.GetProperty(policy, "Rules")
	if err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallPolicyAccessFailed,
			fmt.Sprintf("INetFwPolicy2::Rules failed: %v", err),
		)
	}
	rules := rulesValue.ToIDispatch()
	if rules == nil {
		_ = rulesValue.Clear()
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallPolicyAccessFailed,
			"INetFwPolicy2::Rules returned a non-dispatch value",
		)
	}
	defer rules.Release()
	return fn(policy, rules)
}

func createDispatchObject(programID string) (*ole.IDispatch, error) {
	unknown, err := oleutil.CreateObject(programID)
	if err != nil {
		return nil, err
	}
	defer unknown.Release()
	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}
	return dispatch, nil
}

func ensureLocalPolicyRulesTakeEffect(policy *ole.IDispatch) error {
	value, err := oleutil.GetProperty(policy, "LocalPolicyModifyState")
	if err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallPolicyAccessFailed,
			fmt.Sprintf("INetFwPolicy2::LocalPolicyModifyState failed: %v", err),
		)
	}
	defer value.Clear()
	if state, ok := variantInt(value); ok && state == netFwModifyStateOK {
		return nil
	}
	return windowssandbox.NewSetupFailure(
		windowssandbox.SetupErrorHelperFirewallPolicyIneffective,
		fmt.Sprintf("local firewall policy modifications will not take effect: LocalPolicyModifyState=%v", value.Value()),
	)
}

func removeFirewallRuleIfPresent(rules *ole.IDispatch, internalName string, log io.Writer) error {
	rule, err := firewallRuleByName(rules, internalName)
	if err != nil {
		return nil
	}
	if rule != nil {
		rule.Release()
	}
	value, err := oleutil.CallMethod(rules, "Remove", internalName)
	if value != nil {
		_ = value.Clear()
	}
	if err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed,
			fmt.Sprintf("Rules::Remove failed for %s: %v", internalName, err),
		)
	}
	logFirewallLine(log, fmt.Sprintf("firewall rule removed name=%s", internalName))
	return nil
}

func ensureFirewallBlockRule(rules *ole.IDispatch, spec firewallBlockRuleSpec, log io.Writer) error {
	rule, err := firewallRuleByName(rules, spec.internalName)
	if err != nil {
		rule, err = createDispatchObject("HNetCfg.FWRule")
		if err != nil {
			return windowssandbox.NewSetupFailure(
				windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed,
				fmt.Sprintf("CoCreateInstance HNetCfg.FWRule failed: %v", err),
			)
		}
		if err := putFirewallProperty(rule, "Name", spec.internalName); err != nil {
			rule.Release()
			return firewallRuleMutationFailure("SetName", err)
		}
		if err := configureFirewallRule(rule, spec); err != nil {
			rule.Release()
			return err
		}
		added, addErr := oleutil.CallMethod(rules, "Add", rule)
		if added != nil {
			_ = added.Clear()
		}
		if addErr != nil {
			rule.Release()
			return windowssandbox.NewSetupFailure(
				windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed,
				fmt.Sprintf("Rules::Add failed: %v", addErr),
			)
		}
	}
	defer rule.Release()
	if err := configureFirewallRule(rule, spec); err != nil {
		return err
	}
	remoteAddresses := "*"
	if spec.remoteAddressIsSet {
		remoteAddresses = spec.remoteAddresses
	}
	remotePorts := "*"
	if spec.remotePortsIsSet {
		remotePorts = spec.remotePorts
	}
	logFirewallLine(log, fmt.Sprintf(
		"firewall rule configured name=%s protocol=%d RemoteAddresses=%s RemotePorts=%s LocalUserAuthorizedList=%s",
		spec.internalName, spec.protocol, remoteAddresses, remotePorts, spec.localUserSpec,
	))
	return nil
}

func firewallRuleByName(rules *ole.IDispatch, name string) (*ole.IDispatch, error) {
	value, err := oleutil.GetProperty(rules, "Item", name)
	if err != nil {
		value, err = oleutil.CallMethod(rules, "Item", name)
	}
	if err != nil {
		return nil, err
	}
	rule := value.ToIDispatch()
	if rule == nil {
		_ = value.Clear()
		return nil, fmt.Errorf("Rules::Item(%s) returned a non-dispatch value", name)
	}
	return rule, nil
}

func configureFirewallRule(rule *ole.IDispatch, spec firewallBlockRuleSpec) error {
	steps := []struct {
		name  string
		value interface{}
	}{
		{"Description", spec.friendlyDesc},
		{"Direction", int32(netFwRuleDirOut)},
		{"Action", int32(netFwActionBlock)},
		{"Enabled", true},
		{"Profiles", int32(netFwProfile2All)},
		{"Protocol", int32(spec.protocol)},
	}
	for _, step := range steps {
		if err := putFirewallProperty(rule, step.name, step.value); err != nil {
			return firewallRuleMutationFailure("Set"+step.name, err)
		}
	}
	if spec.remoteAddressIsSet {
		if err := putFirewallProperty(rule, "RemoteAddresses", spec.remoteAddresses); err != nil {
			return firewallRuleMutationFailure("SetRemoteAddresses", err)
		}
	}
	if spec.remotePortsIsSet {
		if err := putFirewallProperty(rule, "RemotePorts", spec.remotePorts); err != nil {
			return firewallRuleMutationFailure("SetRemotePorts", err)
		}
	}
	if err := putFirewallProperty(rule, "LocalUserAuthorizedList", spec.localUserSpec); err != nil {
		return firewallRuleMutationFailure("SetLocalUserAuthorizedList", err)
	}
	actual, err := oleutil.GetProperty(rule, "LocalUserAuthorizedList")
	if err != nil {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallRuleVerifyFailed,
			fmt.Sprintf("LocalUserAuthorizedList (read-back) failed: %v", err),
		)
	}
	defer actual.Clear()
	actualString := actual.ToString()
	if !strings.Contains(actualString, spec.offlineSID) {
		return windowssandbox.NewSetupFailure(
			windowssandbox.SetupErrorHelperFirewallRuleVerifyFailed,
			fmt.Sprintf("offline firewall rule user scope mismatch: expected SID %s, got %s", spec.offlineSID, actualString),
		)
	}
	return nil
}

func putFirewallProperty(dispatch *ole.IDispatch, name string, value interface{}) error {
	result, err := oleutil.PutProperty(dispatch, name, value)
	if result != nil {
		_ = result.Clear()
	}
	return err
}

func firewallRuleMutationFailure(operation string, err error) error {
	return windowssandbox.NewSetupFailure(
		windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed,
		fmt.Sprintf("%s failed: %v", operation, err),
	)
}

func variantInt(value *ole.VARIANT) (int32, bool) {
	if value == nil {
		return 0, false
	}
	switch typed := value.Value().(type) {
	case int8:
		return int32(typed), true
	case int16:
		return int32(typed), true
	case int32:
		return typed, true
	case int:
		return int32(typed), true
	case uint8:
		return int32(typed), true
	case uint16:
		return int32(typed), true
	case uint32:
		if typed > uint32(^uint32(0)>>1) {
			return 0, false
		}
		return int32(typed), true
	case uint:
		return int32(typed), true
	default:
		return 0, false
	}
}

func logFirewallLine(log io.Writer, line string) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintln(log, line)
}
