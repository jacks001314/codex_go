package windowssandbox

import (
	"testing"
	"unsafe"
)

// Mirrors Rust's `only_exact_opt_in_requests_registered_core`. Rust also covers
// an unpaired-surrogate OsStr value; Go's os.LookupEnv yields an already decoded
// string, so that input cannot be expressed here.
func TestOnlyExactOptInRequestsRegisteredCore(t *testing.T) {
	cases := []struct {
		value   string
		present bool
		want    bool
	}{
		{"", false, false},
		{"", true, false},
		{"0", true, false},
		{"1", true, true},
		{"true", true, false},
		{" 1", true, false},
		{"1 ", true, false},
	}
	for _, tt := range cases {
		if got := RequestedValue(tt.value, tt.present); got != tt.want {
			t.Fatalf("RequestedValue(%q, %v) = %v, want %v", tt.value, tt.present, got, tt.want)
		}
	}
}

// Mirrors Rust's
// `package_query_distinguishes_absence_from_failure_and_bounds_allocation`.
func TestPackageQueryDistinguishesAbsenceFromFailureAndBoundsAllocation(t *testing.T) {
	name, ok, err := QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
		return appModelErrorNoPackage
	}, /*maxLength*/ 256)
	if err != nil || ok || name != "" {
		t.Fatalf("no-package query = %q/%v/%v, want an absent package", name, ok, err)
	}
	// ERROR_ACCESS_DENIED is a failure, not an absent package.
	if _, _, err := QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
		return 5
	}, /*maxLength*/ 256); err == nil {
		t.Fatal("access-denied query must fail")
	}
	for _, maxLength := range []uint32{256, 32768} {
		_, _, err := QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
			if buffer != nil {
				t.Fatal("the first query must pass a nil buffer")
			}
			*length = maxLength + 1
			return errorInsufficientBuffer
		}, maxLength)
		if err == nil {
			t.Fatalf("maxLength %d: an over-long request must fail", maxLength)
		}
	}
}

// Mirrors Rust's
// `package_query_validates_returned_length_termination_and_utf16`.
func TestPackageQueryValidatesReturnedLengthTerminationAndUTF16(t *testing.T) {
	cases := []struct {
		name     string
		value    []uint16
		returned uint32
		want     string
	}{
		{"terminated ascii", []uint16{65, 0}, 2, "A"},
		{"zero length", []uint16{65, 0}, 0, ""},
		{"length past the buffer", []uint16{65, 0}, 3, ""},
		{"unterminated", []uint16{65, 66}, 2, ""},
		{"embedded nul", []uint16{65, 0, 66, 0}, 4, ""},
		{"unpaired surrogate", []uint16{0xd800, 0}, 2, ""},
	}
	for _, tt := range cases {
		name, ok, err := QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
			if buffer == nil {
				*length = uint32(len(tt.value))
				return errorInsufficientBuffer
			}
			copy(unsafe.Slice(buffer, len(tt.value)), tt.value)
			*length = tt.returned
			return errorSuccess
		}, /*maxLength*/ 256)
		if tt.want == "" {
			if err == nil {
				t.Fatalf("%s: value %v returned %d = %q, want a failure", tt.name, tt.value, tt.returned, name)
			}
			continue
		}
		if err != nil || !ok || name != tt.want {
			t.Fatalf("%s: value %v returned %d = %q/%v/%v, want %q", tt.name, tt.value, tt.returned, name, ok, err, tt.want)
		}
	}
	// A failed read after a successful first query is reported.
	if _, _, err := QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
		*length = 2
		if buffer == nil {
			return errorInsufficientBuffer
		}
		return 5
	}, /*maxLength*/ 256); err == nil {
		t.Fatal("a failed second query must be reported")
	}
}
