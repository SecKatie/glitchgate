// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/base64"
	"strings"
)

const geminiThoughtSignatureMarker = "__ggts__"

// encodeGeminiToolCallID stashes Gemini's opaque thoughtSignature inside a tool
// call identifier that clients already round-trip unchanged.
func encodeGeminiToolCallID(id, thoughtSignature string) string {
	if thoughtSignature == "" || strings.Contains(id, geminiThoughtSignatureMarker) {
		return id
	}
	if id == "" {
		id = "call_gemini"
	}
	return id + geminiThoughtSignatureMarker +
		base64.RawURLEncoding.EncodeToString([]byte(thoughtSignature))
}

// decodeGeminiToolCallID restores a previously embedded thoughtSignature.
func decodeGeminiToolCallID(id string) (baseID, thoughtSignature string) {
	baseID = id
	idx := strings.Index(id, geminiThoughtSignatureMarker)
	if idx < 0 {
		return baseID, ""
	}

	encoded := id[idx+len(geminiThoughtSignatureMarker):]
	if encoded == "" {
		return baseID, ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return baseID, ""
	}
	return id[:idx], string(decoded)
}
