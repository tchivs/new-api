package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func disableChannelTestResponse(t *testing.T, statusCode int, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func withAutomaticDisableChannel(t *testing.T, enabled bool) {
	t.Helper()
	original := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = original })
	common.AutomaticDisableChannelEnabled = enabled
}

// A DeepSeek "Insufficient Balance" reply means the key is dead: the channel
// must leave the pool so retries stop landing on it. Regression for the
// deployed case where a 402 stayed enabled because 402 was outside the disable
// range and the wording was outside the keyword list.
func TestShouldDisableChannelOnInsufficientBalance(t *testing.T) {
	withAutomaticDisableChannel(t, true)

	resp := disableChannelTestResponse(t, http.StatusPaymentRequired,
		`{"error":{"message":"Insufficient Balance","type":"insufficient_balance"}}`)

	require.True(t, ShouldDisableChannel(RelayErrorHandler(t.Context(), resp, false)))

	// Relays and reseller upstreams surface the same condition under other
	// status codes, so the keyword must carry the decision on its own.
	resp = disableChannelTestResponse(t, http.StatusForbidden,
		`{"error":{"message":"Insufficient Balance","type":"insufficient_balance"}}`)

	require.True(t, ShouldDisableChannel(RelayErrorHandler(t.Context(), resp, false)))
}

// A 400 stays explicit-only: the keyword list must not grow into disabling
// every channel that reports a client-side request bug.
func TestShouldNotDisableChannelOnBadRequest(t *testing.T) {
	withAutomaticDisableChannel(t, true)

	resp := disableChannelTestResponse(t, http.StatusBadRequest,
		`{"error":{"message":"The reasoning_text in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`)

	require.False(t, ShouldDisableChannel(RelayErrorHandler(t.Context(), resp, false)))
}
