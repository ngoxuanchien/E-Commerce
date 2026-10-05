package domain

import "testing"

func TestCapture(t *testing.T) {
	if Capture(false) != CaptureAsCapturing {
		t.Error("Capture(false) should capture")
	}
	if Capture(true) != CaptureAsDuplicate {
		t.Error("Capture(true) should be duplicate")
	}
}

func TestRefundDue(t *testing.T) {
	tests := []struct{ captured, cancelled, want bool }{
		{false, false, false},
		{false, true, false},
		{true, false, false},
		{true, true, true},
	}
	for _, tc := range tests {
		if got := RefundDue(tc.captured, tc.cancelled); got != tc.want {
			t.Errorf("RefundDue(%v,%v) = %v, want %v", tc.captured, tc.cancelled, got, tc.want)
		}
	}
}
