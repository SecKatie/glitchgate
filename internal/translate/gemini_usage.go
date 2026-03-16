// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// GeminiUsageTotals normalizes Gemini usageMetadata into the proxy's internal
// accounting model:
//   - input tokens exclude cache hits
//   - cacheRead tokens track cachedContentTokenCount
//   - output tokens include all output, including reasoning
//   - reasoning tokens are the reasoning subset of output
func GeminiUsageTotals(md *GeminiUsageMetadata) (input, output, cacheRead, reasoning int64) {
	if md == nil {
		return 0, 0, 0, 0
	}

	input = md.PromptTokenCount
	output = md.CandidatesTokenCount + md.ThoughtsTokenCount
	cacheRead = md.CachedContentTokenCount
	reasoning = md.ThoughtsTokenCount

	if cacheRead < 0 {
		cacheRead = 0
	}
	if cacheRead > input {
		cacheRead = input
	}
	input -= cacheRead

	return input, output, cacheRead, reasoning
}
