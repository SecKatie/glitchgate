// SPDX-License-Identifier: AGPL-3.0-or-later

package gemini

import (
	"encoding/base64"
	"strings"
)

// GeminiRequest is the request body for Vertex AI Gemini generateContent.
type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []GeminiTool            `json:"tools,omitempty"`
	SafetySettings    []GeminiSafetySetting   `json:"safetySettings,omitempty"`
}

// GeminiContent represents a message in a conversation.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart is a single segment of content within a message.
type GeminiPart struct {
	Text             string              `json:"text,omitempty"`
	InlineData       *GeminiBlob         `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFuncResponse `json:"functionResponse,omitempty"`
	Thought          *bool               `json:"thought,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
}

// GeminiBlob holds inline binary data (images, etc.).
type GeminiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiFunctionCall represents a function call predicted by the model.
type GeminiFunctionCall struct {
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

// GeminiFuncResponse is the result of a function call.
type GeminiFuncResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// GeminiGenerationConfig holds generation parameters.
type GeminiGenerationConfig struct {
	MaxOutputTokens *int                  `json:"maxOutputTokens,omitempty"`
	Temperature     *float64              `json:"temperature,omitempty"`
	TopP            *float64              `json:"topP,omitempty"`
	TopK            *int                  `json:"topK,omitempty"`
	StopSequences   []string              `json:"stopSequences,omitempty"`
	ThinkingConfig  *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// GeminiThinkingConfig controls the model's thinking/reasoning.
type GeminiThinkingConfig struct {
	ThinkingBudget *int `json:"thinkingBudget,omitempty"`
}

// GeminiTool describes a tool available to the model.
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// GeminiFunctionDeclaration describes a single function the model can call.
type GeminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// GeminiSafetySetting configures content safety thresholds.
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiResponse is the response from generateContent.
type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// GeminiCandidate is a single generated response candidate.
type GeminiCandidate struct {
	Content       *GeminiContent       `json:"content,omitempty"`
	FinishReason  string               `json:"finishReason,omitempty"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

// GeminiSafetyRating is a safety assessment for generated content.
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// GeminiUsageMetadata reports token consumption.
type GeminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount,omitempty"`
}

// ---------------------------------------------------------------------------
// Gemini helper functions
// ---------------------------------------------------------------------------

const GeminiThoughtSignatureMarker = "__ggts__"

// EncodeGeminiToolCallID stashes Gemini's opaque thoughtSignature inside a tool
// call identifier that clients already round-trip unchanged.
func EncodeGeminiToolCallID(id, thoughtSignature string) string {
	if thoughtSignature == "" || strings.Contains(id, GeminiThoughtSignatureMarker) {
		return id
	}
	if id == "" {
		id = "call_gemini"
	}
	return id + GeminiThoughtSignatureMarker +
		base64.RawURLEncoding.EncodeToString([]byte(thoughtSignature))
}

// DecodeGeminiToolCallID restores a previously embedded thoughtSignature.
func DecodeGeminiToolCallID(id string) (baseID, thoughtSignature string) {
	baseID = id
	idx := strings.Index(id, GeminiThoughtSignatureMarker)
	if idx < 0 {
		return baseID, ""
	}

	encoded := id[idx+len(GeminiThoughtSignatureMarker):]
	if encoded == "" {
		return baseID, ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return baseID, ""
	}
	return id[:idx], string(decoded)
}

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

// GeminiUnsupportedSchemaKeys lists JSON Schema keywords rejected by Gemini's
// OpenAPI-3.0-based Schema type. Any key starting with "$" is also dropped.
var GeminiUnsupportedSchemaKeys = map[string]bool{
	"additionalProperties":  true,
	"definitions":           true,
	"propertyNames":         true,
	"const":                 true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"if":                    true,
	"then":                  true,
	"else":                  true,
	"not":                   true,
	"contains":              true,
	"unevaluatedProperties": true,
	"unevaluatedItems":      true,
	"prefixItems":           true,
	"dependentSchemas":      true,
	"dependentRequired":     true,
	"patternProperties":     true,
}

// SanitizeSchemaForGemini removes JSON Schema keywords not supported by the
// Gemini API's OpenAPI-3.0-based Schema type. It drops any field whose name
// starts with "$" (e.g. "$schema", "$defs") and all keys in
// GeminiUnsupportedSchemaKeys. Operates recursively; non-map values are
// returned unchanged.
func SanitizeSchemaForGemini(schema any) any {
	switch v := schema.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if len(key) > 0 && key[0] == '$' {
				continue
			}
			if GeminiUnsupportedSchemaKeys[key] {
				continue
			}
			out[key] = SanitizeSchemaForGemini(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = SanitizeSchemaForGemini(item)
		}
		return out
	default:
		return schema
	}
}
