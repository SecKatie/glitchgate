// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"log/slog"
	"time"

	"github.com/seckatie/glitchgate/internal/store"
)

// BudgetChecker performs pre-flight budget checks before upstream dispatch.
type BudgetChecker struct {
	store store.BudgetCheckStore
	tz    *time.Location
}

// NewBudgetChecker creates a budget checker that enforces spend limits.
func NewBudgetChecker(s store.BudgetCheckStore, tz *time.Location) *BudgetChecker {
	return &BudgetChecker{store: s, tz: tz}
}

// Check performs the pre-flight budget check for the given proxy key.
// Returns nil if all budgets pass or no budgets are configured.
// Returns a BudgetViolation if any budget is exceeded.
// On database errors, logs a warning and returns nil (fail open).
func (bc *BudgetChecker) Check(ctx context.Context, proxyKeyID string) (*store.BudgetViolation, error) {
	if proxyKeyID == "" {
		return nil, nil
	}

	budgets, err := bc.store.GetApplicableBudgets(ctx, proxyKeyID)
	if err != nil {
		slog.Warn("budget check: failed to get applicable budgets", "error", err)
		return nil, nil //nolint:nilerr // fail open by design
	}
	if len(budgets) == 0 {
		return nil, nil
	}

	now := time.Now()
	for _, b := range budgets {
		start := PeriodStart(b.Period, now, bc.tz)

		spend, err := bc.store.GetSpendSince(ctx, b.Scope, b.ScopeID, start)
		if err != nil {
			slog.Warn("budget check: failed to get spend", "scope", b.Scope, "error", err)
			continue // fail open for individual scope errors
		}

		if spend >= b.LimitUSD {
			return &store.BudgetViolation{
				Scope:    b.Scope,
				ScopeID:  b.ScopeID,
				Period:   b.Period,
				LimitUSD: b.LimitUSD,
				SpendUSD: spend,
				ResetAt:  PeriodResetAt(b.Period, now, bc.tz),
			}, nil
		}
	}

	return nil, nil
}

// PeriodStart returns the start of the current budget period in UTC.
func PeriodStart(period string, now time.Time, tz *time.Location) time.Time {
	local := now.In(tz)
	y, m, d := local.Date()

	switch period {
	case "daily":
		return time.Date(y, m, d, 0, 0, 0, 0, tz).UTC()
	case "weekly":
		weekday := local.Weekday()
		daysBack := (int(weekday) - int(time.Monday) + 7) % 7
		monday := time.Date(y, m, d-daysBack, 0, 0, 0, 0, tz)
		return monday.UTC()
	case "monthly":
		return time.Date(y, m, 1, 0, 0, 0, 0, tz).UTC()
	default:
		// Unknown period — treat as daily.
		return time.Date(y, m, d, 0, 0, 0, 0, tz).UTC()
	}
}

// PeriodResetAt returns the start of the next budget period in UTC.
func PeriodResetAt(period string, now time.Time, tz *time.Location) time.Time {
	local := now.In(tz)
	y, m, d := local.Date()

	switch period {
	case "daily":
		return time.Date(y, m, d+1, 0, 0, 0, 0, tz).UTC()
	case "weekly":
		weekday := local.Weekday()
		daysToMonday := (int(time.Monday) - int(weekday) + 7) % 7
		if daysToMonday == 0 {
			daysToMonday = 7
		}
		return time.Date(y, m, d+daysToMonday, 0, 0, 0, 0, tz).UTC()
	case "monthly":
		return time.Date(y, m+1, 1, 0, 0, 0, 0, tz).UTC()
	default:
		return time.Date(y, m, d+1, 0, 0, 0, 0, tz).UTC()
	}
}
