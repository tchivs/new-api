package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func reasoningItems(t *testing.T, input string) []map[string]any {
	t.Helper()
	var items []map[string]any
	require.NoError(t, common.Unmarshal([]byte(input), &items))
	return items
}

// DeepSeek rejects the whole turn when a reasoning item has no reasoning_text,
// and rejects it again when that item is dropped instead. Summary-only items are
// what OpenAI reasoning items look like, so a conversation that mixes models
// always fails without this backfill.
func TestBackfillDeepSeekResponsesReasoningText(t *testing.T) {
	input := `[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
	  {"type":"reasoning","summary":[{"type":"summary_text","text":"first part. "},{"type":"summary_text","text":"second part."}]},
	  {"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"real reasoning"}]},
	  {"type":"reasoning"},
	  {"type":"function_call","name":"noop","call_id":"call_1","arguments":"{}"}
	]`

	request := dto.OpenAIResponsesRequest{Input: []byte(input)}
	backfillDeepSeekResponsesReasoningText(&request)

	items := reasoningItems(t, string(request.Input))
	require.Len(t, items, 5)

	// Untouched items keep their shape.
	require.Equal(t, "message", items[0]["type"])
	require.Equal(t, []any{map[string]any{"type": "input_text", "text": "hi"}}, items[0]["content"])
	require.Equal(t, "function_call", items[4]["type"])

	// Summary-only reasoning gets its text back from the summary.
	require.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": "first part. second part."}}, items[1]["content"])

	// An item that already carries reasoning_text is not rewritten.
	require.Equal(t, "rs_1", items[2]["id"])
	require.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": "real reasoning"}}, items[2]["content"])

	// A reasoning item with nothing to recover still gets passable text.
	require.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": deepSeekReasoningTextPlaceholder}}, items[3]["content"])
}

func TestBackfillDeepSeekResponsesReasoningTextLeavesOtherPayloadsAlone(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "string input", input: `"just a prompt"`},
		{name: "empty array", input: `[]`},
		{name: "no reasoning items", input: `[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`},
		{name: "null content on non-reasoning item", input: `[{"type":"message","role":"assistant","content":null}]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := dto.OpenAIResponsesRequest{Input: []byte(tc.input)}
			backfillDeepSeekResponsesReasoningText(&request)
			require.JSONEq(t, tc.input, string(request.Input))
		})
	}

	request := dto.OpenAIResponsesRequest{}
	backfillDeepSeekResponsesReasoningText(&request)
	require.Empty(t, request.Input)
}
