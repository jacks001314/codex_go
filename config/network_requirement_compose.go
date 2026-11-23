package config

import (
	"fmt"
	"net"
	"reflect"
	"strings"

	"codex_go/network"
)

// Rust #62882 (codex-rs/config/src/requirements_layers/network.rs): managed
// network requirements compose across sources. The highest-priority list of
// each kind replaces lower lists of that kind, keyed `domains` entries update
// individual selectors by source priority, and a deny wins when a selector
// appears in both surviving lists. A surviving `allowed_domains = []` clears
// lower-priority grants, while an empty `domains` map contributes no updates.

const (
	managedNetworkAllowsKey  = "allowed_domains"
	managedNetworkDeniesKey  = "denied_domains"
	managedNetworkDomainsKey = "domains"
)

// managedNetworkRequirementLayer is one requirement source plus the raw
// requirement values that declare its network policy. Values may contain the
// `network_proxy` / `experimental_network` spelling and `permissions.network`.
type managedNetworkRequirementLayer struct {
	Name   string
	Values map[string]any
}

type managedNetworkDomainInput struct {
	name   string
	fields map[string]any
}

// managedNetworkRequirementLayerValues keeps only the network declarations of a
// requirements document, so a later managed layer can compose them.
func managedNetworkRequirementLayerValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := map[string]any{}
	if raw, ok := values["network_proxy"]; ok {
		out["network_proxy"] = cloneMapValue(raw)
	}
	if raw, ok := values["experimental_network"]; ok {
		out["network_proxy"] = cloneMapValue(raw)
	}
	if permissions, ok := values["permissions"].(map[string]any); ok {
		if raw, ok := permissions["network"]; ok {
			out["permissions"] = map[string]any{"network": cloneMapValue(raw)}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMapValue(value any) any {
	if table, ok := value.(map[string]any); ok {
		return cloneMap(table)
	}
	return value
}

// composeManagedNetworkRequirementLayers composes the managed network policy of
// each source in ascending priority order (later sources win). It returns nil
// when no source declares a network policy, and an empty table when only an
// empty declaration survives.
func composeManagedNetworkRequirementLayers(layers []managedNetworkRequirementLayer) (map[string]any, error) {
	merged := map[string]any{}
	var sources []string
	var inputs []managedNetworkDomainInput
	var nestedInputs []managedNetworkDomainInput
	nestedActive := false
	ambiguousMirror := ""
	hasEmptyMap := false
	hasLegacy := false
	hasNested := false
	hasSocketPaths := false

	for _, layer := range layers {
		values := cloneMap(layer.Values)
		if err := normalizeNetworkRequirementAliases(values); err != nil {
			return nil, err
		}
		legacyRaw, hasLegacyTable := values["network_proxy"]
		var nestedRaw any
		var hasNestedTable bool
		if permissions, ok := values["permissions"].(map[string]any); ok {
			nestedRaw, hasNestedTable = permissions["network"]
		}
		var table, mirror map[string]any
		var ok bool
		switch {
		case hasLegacyTable && hasNestedTable:
			if table, ok = legacyRaw.(map[string]any); !ok {
				return nil, fmt.Errorf("network requirements must be tables")
			}
			if mirror, ok = nestedRaw.(map[string]any); !ok {
				return nil, fmt.Errorf("network requirements must be tables")
			}
		case hasLegacyTable:
			if table, ok = legacyRaw.(map[string]any); !ok {
				return nil, fmt.Errorf("network requirements must be tables")
			}
		case hasNestedTable:
			if table, ok = nestedRaw.(map[string]any); !ok {
				return nil, fmt.Errorf("network requirements must be tables")
			}
		default:
			continue
		}
		hasLegacy = hasLegacy || hasLegacyTable
		hasNested = hasNested || hasNestedTable
		if managedNetworkTableHasSocketPaths(table) || managedNetworkTableHasSocketPaths(mirror) {
			hasSocketPaths = true
		}
		table = cloneMap(table)
		fields := takeManagedNetworkDomainFields(table)
		var alternative map[string]any
		if mirror != nil {
			other := takeManagedNetworkDomainFields(cloneMap(mirror))
			if _, hasLeft := fields[managedNetworkAllowsKey]; hasLeft {
				if _, hasRight := other[managedNetworkAllowsKey]; hasRight &&
					managedNetworkDomainListEmpty(fields, managedNetworkAllowsKey) != managedNetworkDomainListEmpty(other, managedNetworkAllowsKey) {
					return nil, fmt.Errorf("legacy and permissions.network requirements specify different allowed_domains replacements")
				}
			}
			if managedNetworkDomainListEmpty(fields, managedNetworkAllowsKey) != managedNetworkDomainListEmpty(other, managedNetworkAllowsKey) ||
				managedNetworkDomainListEmpty(fields, managedNetworkDeniesKey) != managedNetworkDomainListEmpty(other, managedNetworkDeniesKey) ||
				(len(fields) > 0 && len(other) > 0 && !sameManagedNetworkDomainOperation(fields, other)) {
				ambiguousMirror = layer.Name
			}
			if len(fields) == 0 {
				fields = other
			} else if len(other) > 0 && !reflect.DeepEqual(fields, other) {
				if !nestedActive {
					nestedInputs = append([]managedNetworkDomainInput(nil), inputs...)
					nestedActive = true
				}
				alternative = other
			}
		}
		if domains, ok := fields[managedNetworkDomainsKey].(map[string]any); ok && len(domains) == 0 {
			hasEmptyMap = true
		} else if len(fields) > 0 {
			if nestedActive {
				chosen := alternative
				if chosen == nil {
					chosen = cloneMap(fields)
				}
				nestedInputs = append(nestedInputs, managedNetworkDomainInput{name: layer.Name, fields: chosen})
			}
			inputs = append(inputs, managedNetworkDomainInput{name: layer.Name, fields: fields})
		}
		sources = append(sources, layer.Name)
		mergeConfigMaps(merged, table)
	}
	if len(sources) == 0 {
		return nil, nil
	}
	source := strings.Join(reverseManagedNetworkSourceNames(sources), ", ")
	mixed := len(sources) > 1 && hasLegacy && hasNested
	if mixed && hasSocketPaths {
		return nil, fmt.Errorf("Unix socket path rules across sources require a single network table spelling (%s)", source)
	}
	if len(inputs) > 1 && ambiguousMirror != "" {
		return nil, fmt.Errorf("mirrored network domain rules must use the same list replacements or per-host updates when composed with another domain source (%s)", ambiguousMirror)
	}
	entries, declared, err := composeManagedNetworkDomainEntries(inputs, hasEmptyMap)
	if err != nil {
		return nil, err
	}
	if nestedActive && len(nestedInputs) > 1 {
		nestedEntries, nestedDeclared, err := composeManagedNetworkDomainEntries(nestedInputs, hasEmptyMap)
		if err != nil {
			return nil, err
		}
		if !sameManagedNetworkDomainRestrictions(entries, declared, nestedEntries, nestedDeclared) {
			return nil, fmt.Errorf("mirrored network domain rules disagree after layer composition (%s)", source)
		}
	}
	if declared {
		table := make(map[string]any, len(entries))
		for pattern, permission := range entries {
			table[pattern] = string(permission)
		}
		merged[managedNetworkDomainsKey] = table
	}
	if mixed {
		if err := validateComposedManagedNetworkDomainPatterns(merged); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
	}
	return merged, nil
}

func reverseManagedNetworkSourceNames(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[len(values)-1-i] = value
	}
	return out
}

func managedNetworkTableHasSocketPaths(table map[string]any) bool {
	if table == nil {
		return false
	}
	_, hasSockets := table["unix_sockets"]
	_, hasLegacy := table["allow_unix_sockets"]
	return hasSockets || hasLegacy
}

func takeManagedNetworkDomainFields(table map[string]any) map[string]any {
	fields := map[string]any{}
	for _, key := range []string{managedNetworkAllowsKey, managedNetworkDeniesKey, managedNetworkDomainsKey} {
		if value, ok := table[key]; ok {
			fields[key] = value
			delete(table, key)
		}
	}
	return fields
}

func managedNetworkDomainListEmpty(fields map[string]any, key string) bool {
	list, ok := fields[key].([]any)
	if !ok {
		return false
	}
	return len(list) == 0
}

// sameManagedNetworkDomainOperation mirrors Rust's same_domain_operation: two
// declarations compose identically when they are equal, both use per-host
// updates, or their list identities match.
func sameManagedNetworkDomainOperation(left, right map[string]any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	_, leftKeyed := left[managedNetworkDomainsKey]
	_, rightKeyed := right[managedNetworkDomainsKey]
	if leftKeyed && rightKeyed {
		return true
	}
	if leftKeyed || rightKeyed {
		return false
	}
	for _, key := range []struct {
		field string
		rule  NetworkPermission
	}{
		{managedNetworkAllowsKey, NetworkAllow},
		{managedNetworkDeniesKey, NetworkDeny},
	} {
		if !sameManagedNetworkListIdentities(left[key.field], right[key.field], key.rule) {
			return false
		}
	}
	return true
}

func sameManagedNetworkListIdentities(left, right any, rule NetworkPermission) bool {
	leftList, leftOK := left.([]any)
	rightList, rightOK := right.([]any)
	if !leftOK || !rightOK {
		return leftOK == rightOK
	}
	if len(leftList) != len(rightList) {
		return false
	}
	leftSet := managedNetworkIdentitySet(leftList, rule)
	rightSet := managedNetworkIdentitySet(rightList, rule)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for identity := range leftSet {
		if _, ok := rightSet[identity]; !ok {
			return false
		}
	}
	return true
}

func managedNetworkIdentitySet(values []any, rule NetworkPermission) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		pattern := strings.TrimSpace(fmt.Sprint(value))
		identity, err := managedNetworkPatternCanonical(rule, pattern)
		if err != nil {
			identity = pattern
		}
		out[identity] = struct{}{}
	}
	return out
}

func managedNetworkPatternCanonical(rule NetworkPermission, pattern string) (string, error) {
	if rule == NetworkDeny {
		return network.CanonicalDenyProxyPattern(pattern)
	}
	return network.CanonicalAllowProxyPattern(pattern)
}

func managedNetworkReviewCanonical(pattern string) (string, error) {
	return network.CanonicalReviewProxyPattern(pattern)
}

// managedNetworkSelectorIdentity mirrors the selector a composition decision
// uses (Rust compose_domains): grants compare by the review identity so a
// wildcard port still collides with the bare host, while denials compare by the
// deny identity.
func managedNetworkSelectorIdentity(rule NetworkPermission, pattern string) (string, error) {
	if rule == NetworkDeny {
		return network.CanonicalDenyProxyPattern(pattern)
	}
	return network.CanonicalReviewProxyPattern(pattern)
}

// composeManagedNetworkDomainEntries ports Rust's compose_domains.
func composeManagedNetworkDomainEntries(inputs []managedNetworkDomainInput, hasEmptyMap bool) (map[string]NetworkPermission, bool, error) {
	if len(inputs) == 0 {
		return nil, hasEmptyMap, nil
	}
	lastAllow, lastDeny := -1, -1
	hasKeyed := false
	for i, input := range inputs {
		if _, ok := input.fields[managedNetworkAllowsKey]; ok {
			lastAllow = i
		}
		if _, ok := input.fields[managedNetworkDeniesKey]; ok {
			lastDeny = i
		}
		if _, ok := input.fields[managedNetworkDomainsKey]; ok {
			hasKeyed = true
		}
	}
	clear := -1
	if lastAllow >= 0 && managedNetworkDomainListEmpty(inputs[lastAllow].fields, managedNetworkAllowsKey) {
		clear = lastAllow
	}

	var entries map[string]NetworkPermission
	if len(inputs) < 2 || !hasKeyed {
		combined := map[string]any{}
		for _, input := range inputs {
			for key, value := range input.fields {
				combined[key] = value
			}
		}
		parsed, err := parseManagedNetworkDomainFields(combined)
		if err != nil {
			return nil, false, err
		}
		entries = parsed
	} else {
		domains := map[string]map[string]NetworkPermission{}
		listDenials := map[string][]string{}
		keyedDenials := map[string]map[string]struct{}{}
		published := map[string]map[string]NetworkPermission{}
		for i, input := range inputs {
			fields := cloneMap(input.fields)
			if i != lastAllow {
				delete(fields, managedNetworkAllowsKey)
			}
			if i != lastDeny {
				delete(fields, managedNetworkDeniesKey)
			}
			_, keyed := fields[managedNetworkDomainsKey]
			lists := !keyed
			rules, err := parseManagedNetworkDomainFields(fields)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", input.name, err)
			}
			updates := map[string]map[string]NetworkPermission{}
			for _, pattern := range sortedStringKeys(rules) {
				rule := rules[pattern]
				selector, err := managedNetworkSelectorIdentity(rule, pattern)
				if err != nil {
					return nil, false, fmt.Errorf("%s: %w", input.name, err)
				}
				if rule == NetworkAllow && clear >= 0 && i < clear {
					continue
				}
				if lists {
					delete(keyedDenials, selector)
					if rule == NetworkDeny {
						listDenials[selector] = append(listDenials[selector], pattern)
					}
				} else if rule == NetworkDeny {
					if keyedDenials[selector] == nil {
						keyedDenials[selector] = map[string]struct{}{}
					}
					keyedDenials[selector][pattern] = struct{}{}
				} else if denials, ok := keyedDenials[selector]; ok {
					delete(denials, pattern)
				}
				if updates[selector] == nil {
					updates[selector] = map[string]NetworkPermission{}
				}
				updates[selector][pattern] = rule
			}
			for _, selector := range sortedStringKeys(updates) {
				entry := updates[selector]
				if lists {
					for _, pattern := range listDenials[selector] {
						entry[pattern] = NetworkDeny
					}
				}
				spellings := published[selector]
				if spellings == nil {
					spellings = map[string]NetworkPermission{}
					published[selector] = spellings
				}
				for pattern, rule := range entry {
					spellings[pattern] = rule
				}
				if managedNetworkEntriesHaveDenial(entry) {
					entry = cloneManagedNetworkEntries(spellings)
				} else if ip := managedNetworkSelectorIPv6(selector); ip != nil {
					if inherited, ok := domains[selector]; ok && managedNetworkEntriesAllAllow(inherited) {
						wildcardPort := network.IsNonPublicProxyIP(ip) && managedNetworkEntriesHaveWildcard(entry)
						for pattern := range inherited {
							normalized := pattern
							if wildcardPort {
								host := network.NormalizeProxyHost(pattern)
								normalized = "[" + host + "]:*"
							}
							entry[normalized] = NetworkAllow
						}
					}
				}
				domains[selector] = entry
			}
		}
		out := map[string]NetworkPermission{}
		for _, selector := range sortedStringKeys(domains) {
			for pattern, rule := range domains[selector] {
				out[pattern] = rule
			}
		}
		for _, selector := range sortedStringKeys(keyedDenials) {
			for pattern := range keyedDenials[selector] {
				out[pattern] = NetworkDeny
			}
		}
		entries = out
	}
	if len(entries) > 0 {
		return entries, true, nil
	}
	if clear >= 0 || hasEmptyMap {
		return map[string]NetworkPermission{}, true, nil
	}
	return nil, false, nil
}

func cloneManagedNetworkEntries(entries map[string]NetworkPermission) map[string]NetworkPermission {
	out := make(map[string]NetworkPermission, len(entries))
	for pattern, rule := range entries {
		out[pattern] = rule
	}
	return out
}

func managedNetworkEntriesHaveDenial(entries map[string]NetworkPermission) bool {
	for _, rule := range entries {
		if rule == NetworkDeny {
			return true
		}
	}
	return false
}

func managedNetworkEntriesAllAllow(entries map[string]NetworkPermission) bool {
	for _, rule := range entries {
		if rule != NetworkAllow {
			return false
		}
	}
	return true
}

func managedNetworkEntriesHaveWildcard(entries map[string]NetworkPermission) bool {
	for pattern := range entries {
		if strings.ContainsAny(pattern, "*?") {
			return true
		}
	}
	return false
}

func managedNetworkSelectorIPv6(selector string) net.IP {
	ip := net.ParseIP(selector)
	if ip == nil || ip.To4() != nil {
		return nil
	}
	return ip
}

func parseManagedNetworkDomainFields(fields map[string]any) (map[string]NetworkPermission, error) {
	entries := map[string]NetworkPermission{}
	if _, keyed := fields[managedNetworkDomainsKey]; keyed {
		if _, hasAllows := fields[managedNetworkAllowsKey]; hasAllows {
			return nil, fmt.Errorf("`domains` cannot be combined with legacy `allowed_domains` or `denied_domains` in managed network requirements")
		}
		if _, hasDenies := fields[managedNetworkDeniesKey]; hasDenies {
			return nil, fmt.Errorf("`domains` cannot be combined with legacy `allowed_domains` or `denied_domains` in managed network requirements")
		}
		table, _ := fields[managedNetworkDomainsKey].(map[string]any)
		for pattern, raw := range table {
			permission, ok := raw.(string)
			if !ok || (permission != string(NetworkAllow) && permission != string(NetworkDeny)) {
				return nil, fmt.Errorf("invalid value for network_proxy.domains.%s: expected `allow` or `deny`", pattern)
			}
			entries[pattern] = NetworkPermission(permission)
		}
		return entries, nil
	}
	for _, pattern := range managedNetworkStringList(fields[managedNetworkAllowsKey]) {
		entries[pattern] = NetworkAllow
	}
	for _, pattern := range managedNetworkStringList(fields[managedNetworkDeniesKey]) {
		entries[pattern] = NetworkDeny
	}
	return entries, nil
}

func sameManagedNetworkDomainRestrictions(left map[string]NetworkPermission, leftDeclared bool, right map[string]NetworkPermission, rightDeclared bool) bool {
	leftHasAllow := false
	leftDenials := map[string]struct{}{}
	for pattern, rule := range left {
		if rule == NetworkAllow {
			leftHasAllow = true
			continue
		}
		identity, err := managedNetworkPatternCanonical(NetworkDeny, pattern)
		if err != nil {
			identity = pattern
		}
		leftDenials[identity] = struct{}{}
	}
	rightHasAllow := false
	rightDenials := map[string]struct{}{}
	for pattern, rule := range right {
		if rule == NetworkAllow {
			rightHasAllow = true
			continue
		}
		identity, err := managedNetworkPatternCanonical(NetworkDeny, pattern)
		if err != nil {
			identity = pattern
		}
		rightDenials[identity] = struct{}{}
	}
	if leftHasAllow != rightHasAllow {
		return false
	}
	if len(leftDenials) != len(rightDenials) {
		return false
	}
	for identity := range leftDenials {
		if _, ok := rightDenials[identity]; !ok {
			return false
		}
	}
	return true
}

func validateComposedManagedNetworkDomainPatterns(table map[string]any) error {
	if domains, ok := table[managedNetworkDomainsKey].(map[string]any); ok {
		for _, pattern := range sortedStringKeys(domains) {
			permission, _ := domains[pattern].(string)
			rule := NetworkPermission(permission)
			if _, err := managedNetworkPatternCanonical(rule, pattern); err != nil {
				return err
			}
		}
	}
	for _, pattern := range managedNetworkStringList(table["review_domains"]) {
		if _, err := managedNetworkReviewCanonical(pattern); err != nil {
			return err
		}
	}
	return nil
}
