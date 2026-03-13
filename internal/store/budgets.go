package store

import "database/sql"

func scanBudgetPolicy(limitUSD sql.NullFloat64, period sql.NullString) BudgetPolicy {
	var policy BudgetPolicy
	if limitUSD.Valid {
		policy.LimitUSD = &limitUSD.Float64
	}
	if period.Valid {
		policy.Period = &period.String
	}
	return policy
}
