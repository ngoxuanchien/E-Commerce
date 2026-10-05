package domain

import "testing"

func TestApply(t *testing.T) {
	const amt = int64(150000)
	pending := func(s ObservedState) Observed {
		return Observed{State: s, Amount: amt, ProviderTxnID: "tx1", Reason: "declined"}
	}
	succ := func(a int64) Observed { return Observed{State: ObservedSucceeded, Amount: a, ProviderTxnID: "tx1"} }

	tests := []struct {
		name       string
		from       Status
		obs        Observed
		wantStatus Status
		wantEffect Effect
		wantTxn    string
		wantReason string
	}{
		{"pending/pending", StatusPending, pending(ObservedPending), StatusPending, EffectNone, "", ""},
		{"pending/failed", StatusPending, pending(ObservedFailed), StatusFailed, EffectFailed, "", "declined"},
		{"pending/expired", StatusPending, pending(ObservedExpired), StatusExpired, EffectNone, "", ""},
		{"pending/succeeded", StatusPending, succ(amt), StatusSucceeded, EffectSucceeded, "tx1", ""},
		{"pending/succeeded mismatch", StatusPending, succ(amt + 1), StatusPending, EffectAmountMismatch, "", ""},
		{"failed/succeeded", StatusFailed, succ(amt), StatusSucceeded, EffectSucceeded, "tx1", ""},
		{"failed/succeeded mismatch", StatusFailed, succ(amt - 1), StatusFailed, EffectAmountMismatch, "", "old"},
		{"expired/succeeded", StatusExpired, succ(amt), StatusSucceeded, EffectSucceeded, "tx1", ""},
		{"expired/succeeded mismatch", StatusExpired, succ(0), StatusExpired, EffectAmountMismatch, "", ""},
		{"failed/pending", StatusFailed, pending(ObservedPending), StatusFailed, EffectNone, "", "old"},
		{"failed/failed", StatusFailed, pending(ObservedFailed), StatusFailed, EffectNone, "", "old"},
		{"failed/expired", StatusFailed, pending(ObservedExpired), StatusFailed, EffectNone, "", "old"},
		{"expired/pending", StatusExpired, pending(ObservedPending), StatusExpired, EffectNone, "", ""},
		{"expired/failed", StatusExpired, pending(ObservedFailed), StatusExpired, EffectNone, "", ""},
		{"expired/expired", StatusExpired, pending(ObservedExpired), StatusExpired, EffectNone, "", ""},
	}
	for _, s := range []Status{StatusSucceeded, StatusDuplicate} {
		for _, o := range []Observed{pending(ObservedPending), pending(ObservedFailed), pending(ObservedExpired), succ(amt), succ(amt + 5)} {
			tests = append(tests, struct {
				name       string
				from       Status
				obs        Observed
				wantStatus Status
				wantEffect Effect
				wantTxn    string
				wantReason string
			}{string(s) + "/anything", s, o, s, EffectNone, "orig", ""})
		}
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := Payment{ID: "p1", CheckoutID: "c1", Amount: amt, Status: tc.from}
			switch tc.from {
			case StatusSucceeded, StatusDuplicate:
				in.ProviderTxnID = "orig"
			case StatusFailed:
				in.FailureReason = "old"
			}
			got, eff := Apply(in, tc.obs)
			if got.Status != tc.wantStatus || eff != tc.wantEffect {
				t.Fatalf("status/effect = %s/%d, want %s/%d", got.Status, eff, tc.wantStatus, tc.wantEffect)
			}
			wantTxn := tc.wantTxn
			if tc.wantTxn == "" {
				wantTxn = in.ProviderTxnID
			}
			if got.ProviderTxnID != wantTxn {
				t.Errorf("ProviderTxnID = %q, want %q", got.ProviderTxnID, wantTxn)
			}
			wantReason := tc.wantReason
			if tc.from == StatusFailed && tc.wantReason == "old" {
				wantReason = "old"
			}
			if got.FailureReason != wantReason {
				t.Errorf("FailureReason = %q, want %q", got.FailureReason, wantReason)
			}
			if got.ID != in.ID || got.Amount != in.Amount || got.CheckoutID != in.CheckoutID {
				t.Errorf("identity fields changed: %+v", got)
			}
		})
	}
}

func TestStatusFinalTerminal(t *testing.T) {
	tests := []struct {
		s               Status
		final, terminal bool
	}{
		{StatusPending, false, false},
		{StatusSucceeded, true, true},
		{StatusFailed, false, true},
		{StatusExpired, false, true},
		{StatusDuplicate, true, true},
	}
	for _, tc := range tests {
		if tc.s.Final() != tc.final || tc.s.Terminal() != tc.terminal {
			t.Errorf("%s: Final=%v Terminal=%v", tc.s, tc.s.Final(), tc.s.Terminal())
		}
	}
}
