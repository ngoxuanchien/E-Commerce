package gen_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
)

var (
	_ orderv1.OrderInternalServer     = orderv1.UnimplementedOrderInternalServer{}
	_ paymentv1.PaymentInternalServer = paymentv1.UnimplementedPaymentInternalServer{}
)

func TestP2MessagesRoundTrip(t *testing.T) {
	in := &orderv1.GetCheckoutResponse{
		CheckoutId: "c", BuyerId: "b", PaymentMethod: "VNPAY", PaymentDeadline: timestamppb.Now(),
		Orders: []*orderv1.CheckoutOrder{{OrderId: "o", SellerUserId: "s", Status: "AWAITING_PAYMENT", Subtotal: 100, Total: 130, CommissionAmount: 5}},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out orderv1.GetCheckoutResponse
	if err := proto.Unmarshal(b, &out); err != nil || !proto.Equal(in, &out) {
		t.Fatalf("round trip: %v", err)
	}
	if paymentv1.Outcome_OUTCOME_PAID.Number() != 2 || paymentv1.Outcome_OUTCOME_NOT_PAID.Number() != 1 {
		t.Fatal("Outcome numbers are part of the contract")
	}
}
