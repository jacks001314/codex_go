package model

import "testing"

type responseItemIDCarrierForTest struct {
	id string
}

func (c responseItemIDCarrierForTest) ResponseItemID() string { return c.id }

func (c responseItemIDCarrierForTest) WithResponseItemID(id string) any {
	c.id = id
	return c
}

func TestStripResponseInputItemIDUsesCarrierInterface(t *testing.T) {
	if got := stripResponseInputItemID(responseItemIDCarrierForTest{id: "call-local"}).(responseItemIDCarrierForTest); got.id != "" {
		t.Fatalf("non-prefixed carrier ID = %q, want stripped", got.id)
	}
	if got := stripResponseInputItemID(responseItemIDCarrierForTest{id: "fco_1"}).(responseItemIDCarrierForTest); got.id != "fco_1" {
		t.Fatalf("prefixed carrier ID = %q, want retained", got.id)
	}
}
