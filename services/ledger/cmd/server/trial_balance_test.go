package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrialBalanceIsReservedUntilWS2(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 2, trialBalance(&out))
	assert.Contains(t, out.String(), "not implemented")
}
