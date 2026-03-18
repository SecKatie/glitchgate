// SPDX-License-Identifier: AGPL-3.0-or-later

package gemini

import (
	"encoding/base64"
	"strings"
)

// Request is the request body for Vertex AI Gemini generateContent.
type Request struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	SafetySettings    []SafetySetting   `json:"safetySettings,omitempty"`
}

// Content represents a message in a conversation.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Part is a single segment of content within a message.
type Part struct {
	Text             string        `json:"text,omitempty"`
	InlineData       *Blob         `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *FuncResponse `json:"functionResponse,omitempty"`
	Thought          *bool         `json:"thought,omitempty"`
	ThoughtSignature string        `json:"thoughtSignature,omitempty"`
}

// Blob holds inline binary data (images, etc.).
type Blob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FunctionCall represents a function call predicted by the model.
type FunctionCall struct {
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

// FuncResponse is the result of a function call.
type FuncResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// GenerationConfig holds generation parameters.
type GenerationConfig struct {
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	TopK            *int            `json:"topK,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
	ThinkingConfig  *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// ThinkingConfig controls the model's thinking/reasoning.
type ThinkingConfig struct {
	ThinkingBudget *int `json:"thinkingBudget,omitempty"`
}

// Tool describes a tool available to the model.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration describes a single function the model can call.
type FunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// SafetySetting configures content safety thresholds.
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// Response is the response from generateContent.
type Response struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string         `json:"modelVersion,omitempty"`
}

// Candidate is a single generated response candidate.
type Candidate struct {
	Content       *Content       `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// SafetyRating is a safety assessment for generated content.
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// UsageMetadata reports token consumption.
type UsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount,omitempty"`
}

// ---------------------------------------------------------------------------
// Gemini helper functions
// ---------------------------------------------------------------------------

// ThoughtSignatureMarker is the delimiter used to embed thought signatures in tool call IDs.
const ThoughtSignatureMarker = "__ggts__"

// EncodeToolCallID stashes Gemini's opaque thoughtSignature inside a tool
// call identifier that clients already round-trip unchanged.
func EncodeToolCallID(id, thoughtSignature string) string {
	if thoughtSignature == "" || strings.Contains(id, ThoughtSignatureMarker) {
		return id
	}
	if id == "" {
		id = "call_gemini"
	}
	return id + ThoughtSignatureMarker +
		base64.RawURLEncoding.EncodeToString([]byte(thoughtSignature))
}

// DecodeToolCallID restores a previously embedded thoughtSignature.
func DecodeToolCallID(id string) (baseID, thoughtSignature string) {
	baseID = id
	idx := strings.Index(id, ThoughtSignatureMarker)
	if idx < 0 {
		return baseID, ""
	}

	encoded := id[idx+len(ThoughtSignatureMarker):]
	if encoded == "" {
		return baseID, ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return baseID, ""
	}
	return id[:idx], string(decoded)
}

// UsageTotals normalizes Gemini usageMetadata into the proxy's internal
// accounting model:
//   - input tokens exclude cache hits
//   - cacheRead tokens track cachedContentTokenCount
//   - output tokens include all output, including reasoning
//   - reasoning tokens are the reasoning subset of output
func UsageTotals(md *UsageMetadata) (input, output, cacheRead, reasoning int64) {
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
