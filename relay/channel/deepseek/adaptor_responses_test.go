package deepseek

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reasoningItems(t *testing.T, input string) []map[string]any {
	t.Helper()
	var items []map[string]any
	require.NoError(t, common.Unmarshal([]byte(input), &items))
	return items
}

// Summary text is retained when plaintext reasoning was not preserved by the client.
func TestBackfillDeepSeekResponsesReasoningText(t *testing.T) {
	input := `[
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
	  {"type":"reasoning","summary":[{"type":"summary_text","text":"first part. "},{"type":"summary_text","text":"second part."}]},
	  {"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"real reasoning"}]},
	  {"type":"reasoning"},
	  {"type":"function_call","name":"noop","call_id":"call_1","arguments":"{}"}
	]`

	request := dto.OpenAIResponsesRequest{Input: []byte(input)}
	require.NoError(t, backfillDeepSeekResponsesReasoningText(&request))

	items := reasoningItems(t, string(request.Input))
	require.Len(t, items, 5)

	// Untouched items keep their shape.
	assert.Equal(t, "message", items[0]["type"])
	assert.Equal(t, []any{map[string]any{"type": "input_text", "text": "hi"}}, items[0]["content"])
	assert.Equal(t, "function_call", items[4]["type"])

	// Summary-only reasoning gets its text back from the summary.
	assert.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": "first part. second part."}}, items[1]["content"])

	// An item that already carries reasoning_text is not rewritten.
	assert.Equal(t, "rs_1", items[2]["id"])
	assert.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": "real reasoning"}}, items[2]["content"])

	// A reasoning item with nothing to recover still gets passable text.
	assert.Equal(t, []any{map[string]any{"type": "reasoning_text", "text": deepSeekReasoningTextPlaceholder}}, items[3]["content"])
}

func TestBackfillDeepSeekResponsesReasoningTextLeavesOtherPayloadsAlone(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "string input", input: `"just a prompt"`},
		{name: "empty array", input: `[]`},
		{name: "no reasoning items", input: `[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := dto.OpenAIResponsesRequest{Input: []byte(tc.input)}
			require.NoError(t, backfillDeepSeekResponsesReasoningText(&request))
			assert.Equal(t, tc.input, string(request.Input))
		})
	}

	request := dto.OpenAIResponsesRequest{}
	require.NoError(t, backfillDeepSeekResponsesReasoningText(&request))
	assert.Empty(t, request.Input)
}

func TestConvertDeepSeekResponsesRequestReasoningTurns(t *testing.T) {
	const fallback = `{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"reasoning unavailable"}]}`
	const user = `{"role":"user","content":"check"}`
	const assistant = `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking"}]}`
	const call = `{"type":"function_call","name":"noop","call_id":"call_1","arguments":"{}"}`
	const parallelCall = `{"type":"function_call","name":"noop","call_id":"call_2","arguments":"{}"}`
	const result = `{"type":"function_call_output","call_id":"call_1","output":"ok"}`
	const parallelResult = `{"type":"function_call_output","call_id":"call_2","output":"ok"}`
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "entire reasoning item missing before assistant",
			input: `[` + user + `,` + assistant + `]`,
			want:  `[` + user + `,` + fallback + `,` + assistant + `]`,
		},
		{
			name:  "tool results reset reasoning for the next call and final message",
			input: `[` + user + `,` + call + `,` + parallelCall + `,` + result + `,` + parallelResult + `,` + call + `,` + result + `,` + assistant + `]`,
			want:  `[` + user + `,` + fallback + `,` + call + `,` + parallelCall + `,` + result + `,` + parallelResult + `,` + fallback + `,` + call + `,` + result + `,` + fallback + `,` + assistant + `]`,
		},
		{
			name:  "consecutive messages and parallel calls share reasoning",
			input: `[` + user + `,` + fallback + `,` + assistant + `,` + assistant + `,` + call + `,` + parallelCall + `]`,
			want:  `[` + user + `,` + fallback + `,` + assistant + `,` + assistant + `,` + call + `,` + parallelCall + `]`,
		},
		{
			name:  "new user turn does not inherit previous reasoning",
			input: `[` + user + `,` + fallback + `,` + assistant + `,` + user + `,{"role":"assistant","content":"done"}]`,
			want:  `[` + user + `,` + fallback + `,` + assistant + `,` + user + `,` + fallback + `,{"role":"assistant","content":"done"}]`,
		},
		{
			name:  "empty text in a nonempty content array is repaired without losing metadata",
			input: `[{"type":"reasoning","id":"rs_1","revision":9007199254740993,"encrypted_content":"opaque","content":[{"type":"reasoning_text","text":""}],"summary":[{"text":"summary"}]},` + assistant + `]`,
			want:  `[{"type":"reasoning","id":"rs_1","revision":9007199254740993,"encrypted_content":"opaque","content":[{"type":"reasoning_text","text":"summary"}],"summary":[{"text":"summary"}]},` + assistant + `]`,
		},
		{
			name:  "null reasoning content gets a placeholder",
			input: `[{"type":"reasoning","content":null},` + assistant + `]`,
			want:  `[{"type":"reasoning","content":[{"type":"reasoning_text","text":"reasoning unavailable"}]},` + assistant + `]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := dto.OpenAIResponsesRequest{Model: "deepseek-v4-flash", Input: []byte(tc.input)}
			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, original)
			require.NoError(t, err)
			got, ok := converted.(dto.OpenAIResponsesRequest)
			require.True(t, ok)
			assert.JSONEq(t, tc.want, string(got.Input))
			assert.Equal(t, tc.input, string(original.Input), "conversion must not mutate retry input")
			if tc.name == "empty text in a nonempty content array is repaired without losing metadata" {
				assert.Contains(t, string(got.Input), `"revision":9007199254740993`)
			}
			again, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, got)
			require.NoError(t, err)
			assert.Equal(t, got.Input, again.(dto.OpenAIResponsesRequest).Input, "conversion must be idempotent")
		})
	}
}
