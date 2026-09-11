package appserver

import (
	"encoding/json"
	"testing"
)

// TestUserVerificationEnrollResponseMetadataRoundTrip mirrors Rust #44877: the
// public credential metadata is optional, absent/null metadata stays nil
// (serializing as explicit null), and populated metadata survives a round trip.
func TestUserVerificationEnrollResponseMetadataRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want UserVerificationEnrollResponse
	}{
		{
			name: "absent metadata",
			raw:  `{"credentialId":"cred-1"}`,
			want: UserVerificationEnrollResponse{CredentialID: "cred-1"},
		},
		{
			name: "null metadata",
			raw:  `{"credentialId":"cred-1","algorithm":null,"publicKey":null}`,
			want: UserVerificationEnrollResponse{CredentialID: "cred-1"},
		},
		{
			name: "populated metadata",
			raw:  `{"credentialId":"cred-1","algorithm":"ecdsaP256Sha256X962","publicKey":"MFkwEwYH"}`,
			want: UserVerificationEnrollResponse{
				CredentialID: "cred-1",
				Algorithm:    userVerificationStringPointer("ecdsaP256Sha256X962"),
				PublicKey:    userVerificationStringPointer("MFkwEwYH"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got UserVerificationEnrollResponse
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.raw, err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var roundTripped UserVerificationEnrollResponse
			if err := json.Unmarshal(encoded, &roundTripped); err != nil {
				t.Fatalf("round trip unmarshal %s: %v", encoded, err)
			}
			if !equalUserVerificationEnrollResponse(roundTripped, tc.want) {
				t.Fatalf("round trip = %#v (json %s), want %#v", roundTripped, encoded, tc.want)
			}
		})
	}

	// Both metadata fields serialize as explicit null when unset, matching
	// Rust's `Option<String>` (no skip_serializing_if).
	encoded, err := json.Marshal(UserVerificationEnrollResponse{CredentialID: "cred-1"})
	if err != nil {
		t.Fatalf("marshal unset metadata: %v", err)
	}
	if string(encoded) != `{"credentialId":"cred-1","algorithm":null,"publicKey":null}` {
		t.Fatalf("unset metadata json = %s", encoded)
	}
}

func userVerificationStringPointer(value string) *string { return &value }

func equalUserVerificationEnrollResponse(a, b UserVerificationEnrollResponse) bool {
	if a.CredentialID != b.CredentialID {
		return false
	}
	return equalOptionalString(a.Algorithm, b.Algorithm) && equalOptionalString(a.PublicKey, b.PublicKey)
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
