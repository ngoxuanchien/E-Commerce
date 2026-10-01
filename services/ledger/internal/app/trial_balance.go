package app

// TrialBalance is the `server trial-balance` report (WS2' §8).
type TrialBalance struct {
	BalancesByType         map[string]int64 `json:"balances_by_type"` // Σ per account type
	Total                  int64            `json:"total"`            // Σ of every entry; must be 0
	UnbalancedTransactions int64            `json:"unbalanced_transactions"`
	LiabilitiesInDebit     int64            `json:"liabilities_in_debit"`
}

// OK reports whether every check passes.
func (r TrialBalance) OK() bool {
	return r.Total == 0 && r.UnbalancedTransactions == 0 && r.LiabilitiesInDebit == 0
}
