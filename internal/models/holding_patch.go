package models

// HoldingPatch contains fields supported by inline and full-form editing.
// Nil means unchanged; a pointer to an empty string or zero clears the value.
type HoldingPatch struct {
	AssetType     *AssetType `json:"asset_type"     validate:"omitempty,oneof=Stock ETF 'TR Fund' Crypto Gold Cash"`
	PriceProvider *string    `json:"price_provider"`
	InstrumentID  *string    `json:"instrument_id"`
	BaseCurrency  *string    `json:"base_currency"  validate:"omitempty,oneof=TRY USD"`
	Name          *string    `json:"name"           validate:"omitempty,min=1"`
	Ticker        *string    `json:"ticker"`
	Basket        *string    `json:"basket"`
	Location      *string    `json:"location"`
	Notes         *string    `json:"notes"`
	Quantity      *float64   `json:"quantity"       validate:"omitempty,gte=0"`
	EstPrice      *float64   `json:"est_price"      validate:"omitempty,gte=0"`
}

// IsEmpty reports whether a patch contains no editable fields.
func (patch *HoldingPatch) IsEmpty() bool {
	return patch.Name == nil && patch.Ticker == nil &&
		patch.Basket == nil && patch.Location == nil && patch.Notes == nil &&
		patch.Quantity == nil &&
		patch.EstPrice == nil && patch.AssetType == nil && patch.PriceProvider == nil &&
		patch.InstrumentID == nil && patch.BaseCurrency == nil
}
