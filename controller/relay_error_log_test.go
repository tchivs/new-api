package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"101"}, adminInfo["use_channel"])

	logs, total, err := model.GetUserLogs(7, model.LogFilter{Type: model.LogTypeError}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Equal(t, channelSnapshot.ChannelId, logs[0].ChannelId)
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}
}

func TestClaudeStreamRetryAndErrorPresentation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	const start = `data: {"type":"message_start","message":{"id":"msg_retry","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}` + "\n\n"
	const text = `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial output"}}` + "\n\n"
	const tool = `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}` + "\n\n"
	const providerError = `data: {"type":"error","error":{"type":"overloaded_error","message":"upstream overloaded"}}` + "\n\n"

	for _, test := range []struct {
		name     string
		format   types.RelayFormat
		prefix   string
		ping     bool
		wantData string
	}{
		{name: "chat start alone commits generation", format: types.RelayFormatOpenAI, prefix: start, wantData: `"role":"assistant"`},
		{name: "chat content then upstream error", format: types.RelayFormatOpenAI, prefix: start + text, wantData: "partial output"},
		{name: "chat tool call then upstream error", format: types.RelayFormatOpenAI, prefix: start + tool, wantData: `"tool_calls"`},
		{name: "native Claude stays native after error", format: types.RelayFormatClaude, prefix: start + text, wantData: "partial output"},
		{name: "chat ping only permits fallback", format: types.RelayFormatOpenAI, ping: true},
		{name: "native Claude ping only permits fallback", format: types.RelayFormatClaude, ping: true},
		{name: "chat prebody error permits fallback", format: types.RelayFormatOpenAI},
		{name: "native Claude prebody error permits fallback", format: types.RelayFormatClaude},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			if test.ping {
				helper.SetEventStreamHeaders(c)
				require.NoError(t, helper.PingData(c))
			}
			info := &relaycommon.RelayInfo{
				RelayFormat: test.format, DisablePing: true,
				OriginModelName: "claude-opus-5",
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5"},
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(test.prefix + providerError)),
			}
			usage, apiErr := claude.ClaudeStreamHandler(c, resp, info)
			committed := test.prefix != ""
			assert.Equal(t, committed, relaycommon.StreamResponseWritten(c))
			assert.Positive(t, info.ReceivedResponseCount, "receiving an upstream error does not commit downstream data")
			require.NotNil(t, info.StreamStatus)
			assert.True(t, info.StreamStatus.ResponseFailed())

			if test.format == types.RelayFormatClaude && committed {
				// Native Claude has already emitted its terminal error and retains
				// its existing partial-usage settlement path, not a successful result.
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				service.MarkRequestPolicySuccess(c, info.StreamStatus)
				assert.False(t, service.RequestPolicy(c).Successful)
				assert.False(t, service.ShouldRetryRelayError(c, apiErr, 1))
			} else {
				require.NotNil(t, apiErr)
				decision := service.DecideRelayRetry(c, apiErr, 1)
				assert.Equal(t, !committed, decision.Action == "retry")
				if committed {
					assert.Equal(t, "stream_response_committed", decision.Reason)
				}
				// This is the controller's presenter after stopping or exhausting
				// retries, including a stream opened only by the earlier ping.
				respondRelayError(c, test.format, nil, apiErr)
			}

			body := recorder.Body.String()
			if committed {
				assert.Contains(t, body, test.wantData)
			}
			assert.NotContains(t, body, "[DONE]", "an upstream error must not look like successful completion")
			if committed || test.ping {
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
				var lastData string
				for line := range strings.SplitSeq(body, "\n") {
					if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
						continue
					}
					require.True(t, strings.HasPrefix(line, "data: "), "raw JSON tail: %q", line)
					lastData = strings.TrimPrefix(line, "data: ")
				}
				var terminal struct {
					Type  string            `json:"type"`
					Error types.OpenAIError `json:"error"`
				}
				require.NoError(t, common.UnmarshalJsonStr(lastData, &terminal))
				assert.Equal(t, "upstream overloaded", terminal.Error.Message)
				assert.Equal(t, "overloaded_error", terminal.Error.Type)
				if test.format == types.RelayFormatClaude {
					assert.Equal(t, "error", terminal.Type)
					assert.Contains(t, body, "event: error\n")
					assert.NotContains(t, body, `"choices"`)
					if committed {
						assert.Contains(t, body, "event: message_start\n")
						assert.Contains(t, body, "event: content_block_delta\n")
					}
				}
			} else {
				assert.Equal(t, http.StatusInternalServerError, recorder.Code)
				assert.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
				var terminal map[string]any
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &terminal))
				assert.Contains(t, terminal, "error")
			}
		})
	}
}

func TestRelayRetryStopsForCanceledClientBeforeChannelErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		code     types.ErrorCode
		deadline bool
	}{
		{name: "canceled request transport failure", code: types.ErrorCodeDoRequestFailed},
		{name: "canceled request channel failure", code: types.ErrorCodeChannelNoAvailableKey},
		{name: "expired request channel failure", code: types.ErrorCodeChannelNoAvailableKey, deadline: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx, cancel := context.WithCancel(context.Background())
			if test.deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
			} else {
				cancel()
			}
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			apiErr := types.NewError(errors.New("upstream unavailable"), test.code)
			decision := service.DecideRelayRetry(c, apiErr, 1)
			assert.Equal(t, "stop", decision.Action)
			assert.Equal(t, "client_canceled", decision.Reason)
			respondRelayError(c, types.RelayFormatOpenAI, nil, apiErr)
			assert.Empty(t, recorder.Body.String())
			assert.False(t, relaycommon.StreamResponseWritten(c))
		})
	}
}

type limitedStreamResponseWriter struct {
	gin.ResponseWriter
	remaining int
}

func (w *limitedStreamResponseWriter) Write(data []byte) (int, error) {
	n := min(w.remaining, len(data))
	if n == 0 {
		return 0, io.ErrClosedPipe
	}
	written, err := w.ResponseWriter.Write(data[:n])
	w.remaining -= written
	if err != nil {
		return written, err
	}
	return written, io.ErrClosedPipe
}

func TestRelayStreamCommitTracksWrittenProtocolData(t *testing.T) {
	for _, test := range []struct {
		name      string
		write     func(*testing.T, *gin.Context)
		committed bool
	}{
		{name: "headers only", write: func(_ *testing.T, c *gin.Context) { helper.SetEventStreamHeaders(c) }},
		{name: "flushed headers only", write: func(t *testing.T, c *gin.Context) {
			helper.SetEventStreamHeaders(c)
			require.NoError(t, helper.FlushWriter(c))
		}},
		{name: "comment ping", write: func(t *testing.T, c *gin.Context) { require.NoError(t, helper.PingData(c)) }},
		{name: "native ping", write: func(t *testing.T, c *gin.Context) {
			require.NoError(t, helper.ClaudeData(c, dto.ClaudeResponse{Type: "ping"}))
		}},
		{name: "serialization failure", write: func(t *testing.T, c *gin.Context) { require.Error(t, helper.ObjectData(c, make(chan int))) }},
		{name: "terminal without text", write: func(_ *testing.T, c *gin.Context) { helper.Done(c) }, committed: true},
		{name: "responses protocol event", write: func(t *testing.T, c *gin.Context) {
			require.NoError(t, helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.created"}, `{"type":"response.created"}`))
		}, committed: true},
		{name: "failed write without bytes", write: func(_ *testing.T, c *gin.Context) {
			c.Writer = &limitedStreamResponseWriter{ResponseWriter: c.Writer}
			_ = helper.StringData(c, `{"choices":[]}`)
		}},
		{name: "partial failed write commits", write: func(_ *testing.T, c *gin.Context) {
			c.Writer = &limitedStreamResponseWriter{ResponseWriter: c.Writer, remaining: 1}
			_ = helper.StringData(c, `{"choices":[]}`)
		}, committed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			test.write(t, c)
			apiErr := types.NewError(errors.New("channel unavailable"), types.ErrorCodeChannelNoAvailableKey)
			decision := service.DecideRelayRetry(c, apiErr, 1)
			assert.Equal(t, !test.committed, decision.Action == "retry", "channel-error retries must respect the semantic commit")
			assert.Equal(t, test.committed, relaycommon.StreamResponseWritten(c))
		})
	}
	require.Error(t, helper.StringData(nil, "unused"))
	assert.False(t, relaycommon.StreamResponseWritten(nil))
	relaycommon.MarkStreamResponseWritten(nil)
}

func TestResponsesStreamRelayErrorPresentation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.NoError(t, helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.created"}, `{"type":"response.created"}`))
	apiErr := types.NewOpenAIError(errors.New("upstream unavailable"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)

	respondRelayError(c, types.RelayFormatOpenAIResponses, nil, apiErr)

	body := recorder.Body.String()
	require.Contains(t, body, "event: error\ndata: ")
	_, terminal, found := strings.Cut(body, "event: error\ndata: ")
	require.True(t, found)
	var event map[string]any
	require.NoError(t, common.UnmarshalJsonStr(strings.TrimSpace(terminal), &event))
	assert.Equal(t, "error", event["type"])
	assert.Equal(t, "bad_response_body", event["code"])
	assert.Equal(t, "upstream unavailable", event["message"])
	assert.NotContains(t, event, "error", "Responses errors use flat error event fields")
	assert.NotContains(t, body, "[DONE]")
}
