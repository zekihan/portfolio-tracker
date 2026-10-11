package price

import (
	"slices"
	"testing"
)

func TestProviderListsHaveStableOrder(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()

	for range 20 {
		t.Run("all", func(t *testing.T) {
			t.Parallel()

			names := make([]string, 0, len(registry.All()))
			for _, getter := range registry.All() {
				names = append(names, getter.Name())
			}

			if !slices.IsSorted(names) {
				t.Errorf("providers are not alphabetical: %v", names)
			}
		})
		t.Run("by type", func(t *testing.T) {
			t.Parallel()

			names := make([]string, 0, len(registry.All()))
			for _, getter := range registry.GetByType("Stock/ETF") {
				names = append(names, getter.Name())
			}

			if len(names) != 4 || !slices.IsSorted(names) {
				t.Errorf("unexpected filtered provider list: %v", names)
			}
		})
		t.Run("JSON", func(t *testing.T) {
			providers, err := registry.GetProvidersJSON()
			if err != nil {
				t.Fatal(err)
			}

			t.Parallel()

			names := make([]string, 0, len(registry.All()))

			for _, provider := range providers {
				name, ok := provider["name"].(string)
				if !ok {
					t.Fatal("missing provider name")
				}

				names = append(names, name)
			}

			if len(names) != len(registry.All()) || !slices.IsSorted(names) {
				t.Errorf("unexpected JSON provider order: %v", names)
			}
		})
	}
}
