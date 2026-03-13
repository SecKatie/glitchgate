// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// effortToBudgetTokens maps an OpenAI reasoning_effort string to an
// Anthropic thinking budget_tokens value. The budget is capped to
// maxTokens-1 since Anthropic requires budget_tokens < max_tokens.
func effortToBudgetTokens(effort string, maxTokens int) int {
	var budget int
	switch effort {
	case "low":
		budget = 1024
	case "medium":
		budget = 5000
	case "high":
		budget = 10000
	default:
		budget = 5000
	}
	if maxTokens > 0 && budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < 1 {
		budget = 1
	}
	return budget
}

// budgetTokensToEffort maps an Anthropic thinking budget_tokens value
// to an OpenAI reasoning_effort string.
func budgetTokensToEffort(budget int) string {
	switch {
	case budget <= 2000:
		return "low"
	case budget <= 7000:
		return "medium"
	default:
		return "high"
	}
}
