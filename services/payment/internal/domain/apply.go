package domain

// Effect is the side effect the caller must carry out after Apply.
type Effect int

const (
	EffectNone           Effect = iota
	EffectFailed                // emit PaymentFailed
	EffectSucceeded             // run Capture
	EffectAmountMismatch        // log error + metric; state unchanged
)

// Apply returns the payment after observing o, and the effect to carry out (spec §3 table).
func Apply(p Payment, o Observed) (Payment, Effect) {
	if p.Status.Final() {
		return p, EffectNone
	}
	if o.State == ObservedSucceeded {
		if o.Amount != p.Amount {
			return p, EffectAmountMismatch
		}
		p.Status = StatusSucceeded
		p.ProviderTxnID = o.ProviderTxnID
		p.FailureReason = ""
		return p, EffectSucceeded
	}
	if p.Status != StatusPending {
		return p, EffectNone
	}
	switch o.State {
	case ObservedFailed:
		p.Status = StatusFailed
		p.FailureReason = o.Reason
		return p, EffectFailed
	case ObservedExpired:
		p.Status = StatusExpired
	}
	return p, EffectNone
}
