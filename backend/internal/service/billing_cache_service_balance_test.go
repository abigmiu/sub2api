//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBalanceBelowEligibilityThreshold(t *testing.T) {
	svc := &BillingCacheService{cfg: &config.Config{}}
	svc.cfg.Billing.MinimumBalanceReserve = 0.01

	require.True(t, svc.balanceBelowEligibilityThreshold(0))
	require.True(t, svc.balanceBelowEligibilityThreshold(0.005))
	require.False(t, svc.balanceBelowEligibilityThreshold(0.01))
	require.False(t, svc.balanceBelowEligibilityThreshold(1))
}
