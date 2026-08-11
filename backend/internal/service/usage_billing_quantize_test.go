//go:build unit

package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageBillingCommandNormalizeQuantizesMonetaryFields(t *testing.T) {
	cmd := &UsageBillingCommand{
		RequestID:           "request-1",
		BalanceCost:         0.000078125,
		SubscriptionCost:    1.234567895,
		APIKeyQuotaCost:     0.000078125,
		APIKeyRateLimitCost: 2.345678994,
		AccountQuotaCost:    3.456789995,
	}
	cmd.Normalize()

	require.Equal(t, 0.00007813, cmd.BalanceCost)
	require.Equal(t, 1.2345679, cmd.SubscriptionCost)
	require.Equal(t, cmd.BalanceCost, cmd.APIKeyQuotaCost)
	require.Equal(t, 2.34567899, cmd.APIKeyRateLimitCost)
	require.Equal(t, 3.45679, cmd.AccountQuotaCost)
}

func TestQuantizeUsageBillingAmountPreservesSpecialValues(t *testing.T) {
	require.True(t, math.IsNaN(QuantizeUsageBillingAmount(math.NaN())))
	require.True(t, math.IsInf(QuantizeUsageBillingAmount(math.Inf(1)), 1))
	require.Zero(t, QuantizeUsageBillingAmount(0))
}
