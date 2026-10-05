package domain

// CaptureDecision says how a succeeded payment is recorded.
type CaptureDecision int

const (
	CaptureAsCapturing CaptureDecision = iota + 1
	CaptureAsDuplicate
)

// Capture decides whether a succeeded payment becomes the checkout's captured
// payment, or a DUPLICATE because another payment for it is already captured.
func Capture(anotherCaptured bool) CaptureDecision {
	if anotherCaptured {
		return CaptureAsDuplicate
	}
	return CaptureAsCapturing
}

// RefundDue: a refund row must exist for an order's allocation exactly when the
// allocation's payment is captured AND the order is cancelled.
func RefundDue(paymentCaptured, orderCancelled bool) bool {
	return paymentCaptured && orderCancelled
}
