package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerTruncatesInvalidJSONBodyInLog(t *testing.T) {
	withDebugEnabled(t, false)

	body := strings.Repeat("b", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, "bad response status code 500", newAPIError.Error())
	require.Contains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), fmt.Sprintf("original_length=%d", len(body)))
	require.NotContains(t, logBuffer.String(), strings.Repeat("b", common.LocalLogContentLimit+1))
}

func TestRelayErrorHandlerKeepsStructuredErrorMessage(t *testing.T) {
	message := strings.Repeat("c", common.LocalLogContentLimit+256)
	body := `{"message":"` + message + `"}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsOpenAIErrorMessage(t *testing.T) {
	message := strings.Repeat("d", common.LocalLogContentLimit+256)
	body := `{"error":{"message":"` + message + `","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsInvalidJSONBodyInDebugLog(t *testing.T) {
	withDebugEnabled(t, true)

	body := strings.Repeat("e", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.NotContains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), body)
}

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()

	oldDebug := common.DebugEnabled
	common.DebugEnabled = enabled
	t.Cleanup(func() {
		common.DebugEnabled = oldDebug
	})
}

type relayErrorTestBody struct {
	io.Reader
	readErr error
	closes  int
}

func (b *relayErrorTestBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}

func (b *relayErrorTestBody) Close() error {
	b.closes++
	return nil
}

func TestRelayErrorHandlerReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		cause    error
		cancel   bool
		wantSkip bool
	}{
		{name: "truncated rate limit", status: http.StatusTooManyRequests, cause: io.ErrUnexpectedEOF},
		{name: "read failure", status: http.StatusBadGateway, cause: errors.New("response read failed")},
		{name: "upstream-controlled trailer error", status: http.StatusBadGateway, cause: errors.New("malformed MIME header line: X@Internal: fixture-trailer-secret")},
		{name: "upstream timeout", status: http.StatusServiceUnavailable, cause: context.DeadlineExceeded},
		{name: "canceled read", status: http.StatusBadRequest, cause: context.Canceled, wantSkip: true},
		{name: "canceled client", status: http.StatusUnauthorized, cause: io.ErrUnexpectedEOF, cancel: true, wantSkip: true},
	} {
		for _, showBody := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/showBody=%t", tc.name, showBody), func(t *testing.T) {
				const partialBody = `{"error":{"message":"Bearer partial-response-secret","type":"server_error"}}`
				body := &relayErrorTestBody{Reader: strings.NewReader(partialBody), readErr: tc.cause}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.cancel {
					cancel()
				}
				var logBuffer bytes.Buffer
				common.LogWriterMu.Lock()
				oldWriter := gin.DefaultErrorWriter
				gin.DefaultErrorWriter = &logBuffer
				common.LogWriterMu.Unlock()
				t.Cleanup(func() {
					common.LogWriterMu.Lock()
					gin.DefaultErrorWriter = oldWriter
					common.LogWriterMu.Unlock()
				})

				apiErr := RelayErrorHandler(ctx, &http.Response{StatusCode: tc.status, Body: body}, showBody)

				require.NotNil(t, apiErr)
				assert.ErrorIs(t, apiErr, tc.cause)
				assert.Equal(t, tc.status, apiErr.StatusCode)
				assert.Equal(t, types.ErrorCodeBadResponseStatusCode, apiErr.GetErrorCode())
				assert.Equal(t, tc.wantSkip, types.IsSkipRetryError(apiErr))
				assert.Equal(t, 1, body.closes)
				message := fmt.Sprintf("bad response status code %d: failed to read response body", tc.status)
				assert.Equal(t, message, apiErr.Error())
				assert.Equal(t, message, apiErr.ToOpenAIError().Message)
				assert.Equal(t, message, apiErr.ToClaudeError().Message)
				assert.NotContains(t, apiErr.Error(), "partial-response-secret")
				assert.NotContains(t, logBuffer.String(), "partial-response-secret")
				assert.NotContains(t, apiErr.MaskSensitiveErrorWithStatusCode(), "fixture-trailer-secret")
				assert.NotContains(t, logBuffer.String(), "fixture-trailer-secret")
			})
		}
	}
}

func TestRelayErrorHandlerFullReadClosesBody(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		message string
	}{
		{name: "invalid JSON", body: "upstream unavailable", message: ""},
		{name: "general error", body: `{"message":"upstream unavailable"}`, message: "upstream unavailable"},
		{name: "OpenAI error", body: `{"error":{"message":"upstream unavailable","type":"server_error"}}`, message: "upstream unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &relayErrorTestBody{Reader: strings.NewReader(tc.body)}
			apiErr := RelayErrorHandler(context.Background(), &http.Response{StatusCode: http.StatusBadGateway, Body: body}, true)

			require.NotNil(t, apiErr)
			assert.Equal(t, 1, body.closes)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.False(t, types.IsSkipRetryError(apiErr))
			if tc.message == "" {
				assert.Equal(t, "bad response status code 502, body: "+tc.body, apiErr.Error())
			} else {
				assert.Equal(t, "bad response status code 502, message: "+tc.message+", body: "+tc.body, apiErr.Error())
			}
		})
	}
}
