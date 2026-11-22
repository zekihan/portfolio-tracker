package models

import "testing"

func TestHoldingPatchIsEmpty(t *testing.T) {
	t.Parallel()

	if patch := (HoldingPatch{}); !patch.IsEmpty() {
		t.Error("zero patch should be empty")
	}

	// A pointer to a zero value is an explicit edit, not an unchanged field.
	zero := 0.0
	empty := ""

	for name, patch := range map[string]HoldingPatch{
		"quantity pointer to zero": {Quantity: &zero},
		"name pointer to empty":    {Name: &empty},
		"ticker set":               {Ticker: &empty},
	} {
		if patch.IsEmpty() {
			t.Errorf("%s: patch with a set field should not be empty", name)
		}
	}
}
