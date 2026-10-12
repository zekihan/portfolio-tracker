package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zekihan/portfolio-tracker/internal/models"
)

func TestJSONValidationPreservesErrorResponses(t *testing.T) {
	t.Parallel()

	const (
		baseCurrencyError = "Key: 'Holding.BaseCurrency' Error:Field validation for 'BaseCurrency' " +
			"failed on the 'oneof' tag"
		nameError          = "Key: 'Holding.Name' Error:Field validation for 'Name' failed on the 'required' tag"
		assetError         = "Key: 'Holding.AssetType' Error:Field validation for 'AssetType' failed on the 'oneof' tag"
		patchNameError     = "Key: 'HoldingPatch.Name' Error:Field validation for 'Name' failed on the 'min' tag"
		patchQuantityError = "Key: 'HoldingPatch.Quantity' Error:Field validation for 'Quantity' " +
			"failed on the 'gte' tag"
		fund = `{"name":"Fund","asset_type":"TR Fund",` + `"base_currency":"TRY"}`
	)

	for _, test := range []struct {
		name, body, want string
		patch            bool
	}{
		{name: "multiple invalid fields", body: `{}`, want: baseCurrencyError + "\n" + nameError + "\n" + assetError},
		{name: "empty patch name", patch: true, body: `{"name":""}`, want: patchNameError},
		{name: "negative patch quantity", patch: true, body: `{"quantity":-1}`, want: patchQuantityError},
		{name: "null patch fields remain unchanged", patch: true, body: `{"name":null,"quantity":null}`},
		{name: "zero patch values", patch: true, body: `{"quantity":0,"est_price":0}`},
		{name: "TR fund and unknown field", body: strings.TrimSuffix(fund, "}") + `,"unknown":true}`},
		{name: "trailing JSON is ignored", body: fund + ` {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
				strings.NewReader(test.body))

			var destination any = new(models.Holding)
			if test.patch {
				destination = new(models.HoldingPatch)
			}

			err := decodeJSON(request, destination)

			actual := ""
			if err != nil {
				actual = err.Error()
			}

			if actual != test.want {
				t.Errorf("validation error = %q, want %q", actual, test.want)
			}
		})
	}
}
