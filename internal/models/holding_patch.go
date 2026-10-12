package models

// HoldingPatch contains only fields supported by inline editing.
// Nil means unchanged; a pointer to an empty string or zero clears the value.
type HoldingPatch struct {
	Name     *string  `json:"name"      validate:"omitempty,min=1"`
	Ticker   *string  `json:"ticker"`
	Basket   *string  `json:"basket"`
	Location *string  `json:"location"`
	Notes    *string  `json:"notes"`
	Quantity *float64 `json:"quantity"  validate:"omitempty,gte=0"`
	EstPrice *float64 `json:"est_price" validate:"omitempty,gte=0"`
}
