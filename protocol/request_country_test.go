package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRequestCountryRejectsMalformedValues(t *testing.T) {
	for _, malformed := range []string{"", "D", "USA", "D1", "T1", "DÉ", "D E", "DE\nX"} {
		if _, err := ParseRequestCountry(malformed); !errors.Is(err, ErrInvalidRequestCountry) {
			t.Fatalf("ParseRequestCountry(%q) error = %v, want invalid", malformed, err)
		}
	}
}

func TestRequestCountryNormalizesAndSerializes(t *testing.T) {
	country, err := ParseRequestCountry(" de ")
	if err != nil {
		t.Fatalf("ParseRequestCountry(de) error = %v", err)
	}
	if code, ok := country.KnownCountry(); !ok || code != "DE" {
		t.Fatalf("KnownCountry() = %q, %v", code, ok)
	}
	data, err := json.Marshal(country)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `"DE"` {
		t.Fatalf("Marshal() = %s", data)
	}

	unknown, err := ParseRequestCountry("xX")
	if err != nil {
		t.Fatalf("ParseRequestCountry(xX) error = %v", err)
	}
	if unknown.IsOmitted() {
		t.Fatal("explicit unknown country must not be omitted")
	}
	data, err = json.Marshal(unknown)
	if err != nil {
		t.Fatalf("Marshal(unknown) error = %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("Marshal(unknown) = %s, want null", data)
	}

	var omitted RequestCountry
	if !omitted.IsOmitted() {
		t.Fatal("zero value must be omitted")
	}
}
