package fake_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"event_id":"e1"}`)
	h := fake.Sign("s3cret", body, now)

	assert.NoError(t, fake.Verify("s3cret", body, h, now))
	assert.NoError(t, fake.Verify("s3cret", body, h, now.Add(300*time.Second)))
	assert.NoError(t, fake.Verify("s3cret", body, h, now.Add(-300*time.Second)))

	assert.ErrorIs(t, fake.Verify("s3cret", []byte(`{"event_id":"e2"}`), h, now), fake.ErrBadSignature)
	assert.ErrorIs(t, fake.Verify("other", body, h, now), fake.ErrBadSignature)
	assert.ErrorIs(t, fake.Verify("s3cret", body, h, now.Add(301*time.Second)), fake.ErrBadSignature)
	assert.ErrorIs(t, fake.Verify("s3cret", body, h, now.Add(-301*time.Second)), fake.ErrBadSignature)
	for _, bad := range []string{"", "garbage", "t=abc,v1=00", "t=1800000000", "v1=00", "t=1800000000,v1=zz"} {
		assert.ErrorIs(t, fake.Verify("s3cret", body, bad, now), fake.ErrBadSignature, bad)
	}
}

func TestEventObserved(t *testing.T) {
	ok := fake.Event{PaymentID: "p1", Outcome: "succeed"}.Observed(150_000)
	assert.Equal(t, domain.Observed{State: domain.ObservedSucceeded, Amount: 150_000, ProviderTxnID: "fake_txn_p1"}, ok)
	bad := fake.Event{PaymentID: "p1", Outcome: "fail"}.Observed(150_000)
	assert.Equal(t, domain.Observed{State: domain.ObservedFailed, Reason: "fake_declined"}, bad)
	pend := fake.Event{PaymentID: "p1", Outcome: "pending"}.Observed(150_000)
	assert.Equal(t, domain.ObservedPending, pend.State)
}
