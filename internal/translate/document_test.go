package translate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// unmarshalOpenAIReq round-trips a ChatCompletionRequest through JSON to
// ensure Content fields are deserialized as interface{} ([]any), matching
// how the proxy handler receives requests.
func unmarshalOpenAIReq(t *testing.T, req *openai.ChatCompletionRequest) *openai.ChatCompletionRequest {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	var out openai.ChatCompletionRequest
	require.NoError(t, json.Unmarshal(b, &out))
	return &out
}

func TestOpenAIToAnthropic_FileContentPart(t *testing.T) {
	t.Parallel()

	contentParts := []map[string]any{
		{
			"type": "file",
			"file": map[string]any{
				"file_data": "data:application/pdf;base64,JVBER",
				"filename":  "report.pdf",
			},
		},
	}

	req := unmarshalOpenAIReq(t, &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: contentParts},
		},
		MaxTokens: func() *int { v := 200; return &v }(),
	})

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, err := parseContentBlocks(result.Messages[0].Content)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "document", blocks[0].Type)
	require.NotNil(t, blocks[0].Source)
	require.Equal(t, "base64", blocks[0].Source.Type)
	require.Equal(t, "application/pdf", blocks[0].Source.MediaType)
	require.Equal(t, "JVBER", blocks[0].Source.Data)
}

func TestCanonical_AnthropicDocumentToOpenAIFile(t *testing.T) {
	t.Parallel()

	contentBlocks := []anthropic.ContentBlock{
		{
			Type: "document",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				MediaType: "application/pdf",
				Data:      "JVBER",
			},
		},
	}
	contentJSON, _ := json.Marshal(contentBlocks)

	anthMsg := anthropic.Message{Role: "user", Content: json.RawMessage(contentJSON)}
	canonMsg, err := anthropicMessageToCanonical(anthMsg)
	require.NoError(t, err)
	require.Len(t, canonMsg.Blocks, 1)
	require.Equal(t, BlockDocument, canonMsg.Blocks[0].Type)
	require.Equal(t, "application/pdf", canonMsg.Blocks[0].DocMediaType)
	require.Equal(t, "JVBER", canonMsg.Blocks[0].DocData)

	openAIMsgs := canonicalUserToOpenAI(canonMsg)
	require.Len(t, openAIMsgs, 1)
	parts, ok := openAIMsgs[0].Content.([]openai.ContentPart)
	require.True(t, ok)
	require.Len(t, parts, 1)
	require.Equal(t, "file", parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "data:application/pdf;base64,JVBER", parts[0].File.FileData)
}

func TestCanonical_AnthropicDocumentURLToOpenAI(t *testing.T) {
	t.Parallel()

	contentBlocks := []anthropic.ContentBlock{
		{
			Type: "document",
			Source: &anthropic.ImageSource{
				Type: "url",
				URL:  "https://example.com/doc.pdf",
			},
		},
	}
	contentJSON, _ := json.Marshal(contentBlocks)

	anthMsg := anthropic.Message{Role: "user", Content: json.RawMessage(contentJSON)}
	canonMsg, err := anthropicMessageToCanonical(anthMsg)
	require.NoError(t, err)
	require.Len(t, canonMsg.Blocks, 1)
	require.Equal(t, BlockDocument, canonMsg.Blocks[0].Type)
	require.Equal(t, "https://example.com/doc.pdf", canonMsg.Blocks[0].DocURL)

	openAIMsgs := canonicalUserToOpenAI(canonMsg)
	require.Len(t, openAIMsgs, 1)
	parts, ok := openAIMsgs[0].Content.([]openai.ContentPart)
	require.True(t, ok)
	require.Len(t, parts, 1)
	require.Equal(t, "file", parts[0].Type)
	require.Equal(t, "https://example.com/doc.pdf", parts[0].File.FileData)
}

func TestOpenAIToAnthropic_FileWithTextParts(t *testing.T) {
	t.Parallel()

	contentParts := []any{
		map[string]any{"type": "text", "text": "Please analyze this document:"},
		map[string]any{
			"type": "file",
			"file": map[string]any{
				"file_data": "data:application/pdf;base64,JVBER",
				"filename":  "report.pdf",
			},
		},
	}

	req := unmarshalOpenAIReq(t, &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: contentParts},
		},
		MaxTokens: func() *int { v := 200; return &v }(),
	})

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, err := parseContentBlocks(result.Messages[0].Content)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "Please analyze this document:", blocks[0].Text)
	require.Equal(t, "document", blocks[1].Type)
	require.NotNil(t, blocks[1].Source)
	require.Equal(t, "application/pdf", blocks[1].Source.MediaType)
}

func TestOpenAIToAnthropic_FileEmptyDataSkipped(t *testing.T) {
	t.Parallel()

	contentParts := []any{
		map[string]any{
			"type": "file",
			"file": map[string]any{
				"file_data": "",
				"filename":  "empty.pdf",
			},
		},
		map[string]any{"type": "text", "text": "Hello"},
	}

	req := unmarshalOpenAIReq(t, &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: contentParts},
		},
		MaxTokens: func() *int { v := 200; return &v }(),
	})

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	// Single text block → string content optimization.
	require.Equal(t, "Hello", result.Messages[0].Content)
}

func TestDocumentRoundTrip_OpenAIToAnthropicAndBack(t *testing.T) {
	t.Parallel()

	contentParts := []any{
		map[string]any{
			"type": "file",
			"file": map[string]any{
				"file_data": "data:application/pdf;base64,JVBER",
				"filename":  "test.pdf",
			},
		},
	}

	req := unmarshalOpenAIReq(t, &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: contentParts},
		},
		MaxTokens: func() *int { v := 200; return &v }(),
	})

	// OpenAI → Anthropic
	anthReq, err := OpenAIToAnthropic(req)
	require.NoError(t, err)

	blocks, err := parseContentBlocks(anthReq.Messages[0].Content)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "document", blocks[0].Type)
	require.Equal(t, "application/pdf", blocks[0].Source.MediaType)
	require.Equal(t, "JVBER", blocks[0].Source.Data)

	// Anthropic → canonical → OpenAI (via internal functions)
	canonMsg, err := anthropicMessageToCanonical(anthReq.Messages[0])
	require.NoError(t, err)
	require.Equal(t, BlockDocument, canonMsg.Blocks[0].Type)

	openAIMsgs := canonicalUserToOpenAI(canonMsg)
	require.Len(t, openAIMsgs, 1)
	parts, ok := openAIMsgs[0].Content.([]openai.ContentPart)
	require.True(t, ok)
	require.Equal(t, "file", parts[0].Type)
	require.Equal(t, "data:application/pdf;base64,JVBER", parts[0].File.FileData)
}

func TestResponsesToAnthropic_InputFileBase64(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "input_file", FileData: "data:application/pdf;base64,JVBER", Filename: "test.pdf"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)

	blocks, err := parseContentBlocks(result.Messages[0].Content)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "document", blocks[0].Type)
	require.Equal(t, "base64", blocks[0].Source.Type)
	require.Equal(t, "application/pdf", blocks[0].Source.MediaType)
	require.Equal(t, "JVBER", blocks[0].Source.Data)
}

func TestResponsesToAnthropic_InputFileURL(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "input_file", FileURL: "https://example.com/doc.pdf", Filename: "doc.pdf"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, err := parseContentBlocks(result.Messages[0].Content)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "document", blocks[0].Type)
	require.Equal(t, "url", blocks[0].Source.Type)
	require.Equal(t, "https://example.com/doc.pdf", blocks[0].Source.URL)
}

func TestResponsesToOpenAI_InputFilePassthrough(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "input_file", FileData: "data:application/pdf;base64,JVBER", Filename: "test.pdf"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	parts, ok := result.Messages[0].Content.([]openai.ContentPart)
	require.True(t, ok)
	require.Len(t, parts, 1)
	require.Equal(t, "file", parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "data:application/pdf;base64,JVBER", parts[0].File.FileData)
	require.Equal(t, "test.pdf", parts[0].File.Filename)
}

func TestCanonicalToResponses_DocumentBlock(t *testing.T) {
	t.Parallel()

	contentBlocks := []anthropic.ContentBlock{
		{
			Type: "document",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				MediaType: "application/pdf",
				Data:      "JVBER",
			},
		},
	}
	contentJSON, _ := json.Marshal(contentBlocks)

	anthReq := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 200,
		Messages: []anthropic.Message{
			{Role: "user", Content: json.RawMessage(contentJSON)},
		},
	}

	result, err := AnthropicToResponses(anthReq, "claude-sonnet-4-20250514")
	require.NoError(t, err)

	var items []openai.InputItem
	err = json.Unmarshal(result.Input, &items)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "input_file", items[0].Type)
	require.Equal(t, "data:application/pdf;base64,JVBER", items[0].FileData)
}
