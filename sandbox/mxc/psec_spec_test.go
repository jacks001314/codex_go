package mxc

import (
	"encoding/binary"
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"
)

// The assertions below read the buffer exactly the way flatc-generated code
// would: file identifier, root table, then vtable offsets `4 + 2*slot`. They
// pin the wire layout to Microsoft's ProcessSecurityEnvironment.fbs, so a slot
// reorder or a wrong scalar type fails here instead of at native launch time.
func TestBuildSecurityEnvironmentSpecMatchesPSECLayout(t *testing.T) {
	policy := NativeContainerPolicy{
		Capabilities:   []string{"registryRead"},
		ReadWritePaths: []string{`C:\repo`},
		ReadOnlyPaths:  []string{`C:\repo\.git`},
		DeniedPaths:    []string{`C:\repo\.env`},
		NetworkEgress: NetworkEgressPolicy{
			Default: NetworkActionDeny,
			Allow: []NetworkRule{{
				To: []NetworkPeer{
					{CIDR: NetworkCIDR{Address: "127.0.0.0", PrefixLen: 8}},
					{CIDR: NetworkCIDR{Address: "::1", PrefixLen: 128}},
				},
			}},
		},
		NetworkIngress:   NetworkIngressPolicy{Default: NetworkActionDeny, HostLoopback: NetworkActionAllow},
		NetworkSpecified: true,
		UI:               UIPolicy{Disable: false},
		BaseProcessUI:    BaseProcessUI{Isolation: "desktop"},
	}
	spec := policy.securityEnvironmentSpec()
	if spec.UIRestrictions != 0x3DE {
		t.Fatalf("ui restrictions = 0x%X, want 0x3DE (clipboard+injection+desktop+exit+system+display+ime)", spec.UIRestrictions)
	}
	if spec.DisallowWin32kSystemCalls {
		t.Fatalf("disallow win32k = true, want false for the codex helper policy")
	}

	buf, err := BuildSecurityEnvironmentSpec(spec)
	if err != nil {
		t.Fatalf("BuildSecurityEnvironmentSpec() error = %v", err)
	}
	if got := string(buf[4:8]); got != "PSEC" {
		t.Fatalf("file identifier = %q, want %q", got, "PSEC")
	}
	root := flatbuffers.GetUOffsetT(buf)
	table := &flatbuffers.Table{Bytes: buf, Pos: root}

	major, minor := readVersionStruct(t, table, 0)
	if major != 1 || minor != 1 {
		t.Fatalf("version = %d.%d, want 1.1", major, minor)
	}
	capabilities := readStringField(t, table, 1)
	if capabilities != "registryRead,internetClient,networkLoopback" {
		t.Fatalf("capabilities = %q", capabilities)
	}
	if readBoolField(t, table, 2) {
		t.Fatalf("disallow_win32k_system_calls = true, want false")
	}
	if got := readUint64Field(t, table, 3); got != 0x3DE {
		t.Fatalf("ui_restrictions = 0x%X, want 0x3DE", got)
	}
	assertStringVector(t, table, 4, []string{`C:\repo`})
	assertStringVector(t, table, 5, []string{`C:\repo\.git`})
	assertStringVector(t, table, 6, []string{`C:\repo\.env`})

	network := readTableField(t, table, 7)
	if network == nil {
		t.Fatal("network_policy missing")
	}
	if peer := readStringField(t, network, 2); peer != "MXC-Loopback" {
		t.Fatalf("allowed_appcontainer_peer = %q", peer)
	}
	egress := readTableField(t, network, 1)
	if egress == nil {
		t.Fatal("network_policy.egress missing")
	}
	if action := readByteField(t, egress, 0); action != psecFilterDeny {
		t.Fatalf("egress default_action = %d, want deny", action)
	}
	if count := egress.VectorLen(flatbuffers.UOffsetT(fieldOffset(egress, 1))); count != 1 {
		t.Fatalf("egress allow rules = %d, want 1", count)
	}
}

// TestSecurityEnvironmentSpecOmitsEmptyFields locks the "absent means unset"
// contract: empty path lists and an unset network policy must not emit empty
// vectors the native parser would reject or reinterpret.
func TestSecurityEnvironmentSpecOmitsEmptyFields(t *testing.T) {
	buf, err := BuildSecurityEnvironmentSpec(SecurityEnvironmentSpec{})
	if err != nil {
		t.Fatalf("BuildSecurityEnvironmentSpec() error = %v", err)
	}
	table := &flatbuffers.Table{Bytes: buf, Pos: flatbuffers.GetUOffsetT(buf)}
	if major, minor := readVersionStruct(t, table, 0); major != 1 || minor != 1 {
		t.Fatalf("default version = %d.%d, want 1.1", major, minor)
	}
	for _, slot := range []int{1, 4, 5, 6, 7, 8} {
		if off := fieldOffset(table, slot); off != 0 {
			t.Fatalf("slot %d offset = %d, want absent", slot, off)
		}
	}
}

func readByteField(t *testing.T, table *flatbuffers.Table, slot int) byte {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return 0
	}
	return table.GetByte(flatbuffers.UOffsetT(offset) + table.Pos)
}

func fieldOffset(table *flatbuffers.Table, slot int) int {
	return int(table.Offset(flatbuffers.VOffsetT(4 + 2*slot)))
}

func readVersionStruct(t *testing.T, table *flatbuffers.Table, slot int) (uint16, uint16) {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		t.Fatalf("version struct missing (slot %d)", slot)
	}
	base := int(table.Pos) + offset
	return binary.LittleEndian.Uint16(table.Bytes[base:]), binary.LittleEndian.Uint16(table.Bytes[base+2:])
}

func readStringField(t *testing.T, table *flatbuffers.Table, slot int) string {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return ""
	}
	return table.String(flatbuffers.UOffsetT(offset) + table.Pos)
}

func readBoolField(t *testing.T, table *flatbuffers.Table, slot int) bool {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return false
	}
	return table.GetBool(flatbuffers.UOffsetT(offset) + table.Pos)
}

func readUint64Field(t *testing.T, table *flatbuffers.Table, slot int) uint64 {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return 0
	}
	return table.GetUint64(flatbuffers.UOffsetT(offset) + table.Pos)
}

func readTableField(t *testing.T, table *flatbuffers.Table, slot int) *flatbuffers.Table {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return nil
	}
	nested := &flatbuffers.Table{Bytes: table.Bytes}
	table.Union(nested, flatbuffers.UOffsetT(offset))
	return nested
}

func readTableVector(t *testing.T, table *flatbuffers.Table, slot int) []*flatbuffers.Table {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		return nil
	}
	count := table.VectorLen(flatbuffers.UOffsetT(offset))
	start := int(table.Vector(flatbuffers.UOffsetT(offset)))
	out := make([]*flatbuffers.Table, 0, count)
	for i := 0; i < count; i++ {
		element := start + i*4
		relative := int(flatbuffers.GetUOffsetT(table.Bytes[element:]))
		nested := &flatbuffers.Table{Bytes: table.Bytes, Pos: flatbuffers.UOffsetT(element + relative)}
		out = append(out, nested)
	}
	return out
}

func assertStringVector(t *testing.T, table *flatbuffers.Table, slot int, want []string) {
	t.Helper()
	offset := fieldOffset(table, slot)
	if offset == 0 {
		if len(want) == 0 {
			return
		}
		t.Fatalf("vector at slot %d missing, want %#v", slot, want)
	}
	count := table.VectorLen(flatbuffers.UOffsetT(offset))
	if count != len(want) {
		t.Fatalf("vector at slot %d has %d entries, want %d", slot, count, len(want))
	}
	start := int(table.Vector(flatbuffers.UOffsetT(offset)))
	for i := 0; i < count; i++ {
		element := start + i*4
		relative := int(flatbuffers.GetUOffsetT(table.Bytes[element:]))
		stringOffset := element + relative
		length := int(flatbuffers.GetUOffsetT(table.Bytes[stringOffset:]))
		got := string(table.Bytes[stringOffset+4 : stringOffset+4+length])
		if got != want[i] {
			t.Fatalf("vector[%d] at slot %d = %q, want %q", i, slot, got, want[i])
		}
	}
}
