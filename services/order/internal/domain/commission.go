package domain

// Commission is the platform's share of an online order: floor(subtotal ×
// rateBps / 10000), computed once at checkout (overview §4). Inputs are ≥ 0.
func Commission(subtotal int64, rateBps int32) int64 {
	return subtotal * int64(rateBps) / 10_000
}
