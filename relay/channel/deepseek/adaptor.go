package deepseek

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	adaptor := claude.Adaptor{}
	convertedRequest, err := adaptor.ConvertClaudeRequest(c, info, req)
	if err != nil {
		return nil, err
	}
	claudeRequest, ok := convertedRequest.(*dto.ClaudeRequest)
	if !ok {
		return convertedRequest, nil
	}
	if err := applyDeepSeekV4ClaudeThinkingSuffix(info, claudeRequest); err != nil {
		return nil, err
	}
	backfillDeepSeekClaudeThinkingBlocks(claudeRequest)
	return claudeRequest, nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	fimBaseUrl := info.ChannelBaseUrl
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return fmt.Sprintf("%s/anthropic/v1/messages", info.ChannelBaseUrl), nil
	default:
		if !strings.HasSuffix(info.ChannelBaseUrl, "/beta") {
			fimBaseUrl += "/beta"
		}
		switch info.RelayMode {
		case constant.RelayModeCompletions:
			return fmt.Sprintf("%s/completions", fimBaseUrl), nil
		case constant.RelayModeResponses:
			return fmt.Sprintf("%s/responses", info.ChannelBaseUrl), nil
		default:
			return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
		}
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if err := applyDeepSeekV4OpenAIThinkingSuffix(info, request); err != nil {
		return nil, err
	}
	backfillDeepSeekOpenAIReasoningContent(request)
	return request, nil
}

func applyDeepSeekV4OpenAIThinkingSuffix(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) error {
	modelName := request.Model
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		modelName = info.UpstreamModelName
	}
	if model_setting.ShouldPreserveThinkingSuffix(modelName) || info != nil && model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) {
		return nil
	}
	baseModel, thinkingType, effort, ok := reasoning.ParseDeepSeekV4ThinkingSuffix(modelName)
	if !ok {
		return nil
	}
	thinking, err := common.Marshal(map[string]string{
		"type": thinkingType,
	})
	if err != nil {
		return fmt.Errorf("error marshalling thinking: %w", err)
	}
	request.Model = baseModel
	request.THINKING = thinking
	request.ReasoningEffort = effort
	if info != nil {
		if info.ChannelMeta != nil {
			info.UpstreamModelName = baseModel
		}
		info.SetReasoningEffort(effort)
	}
	return nil
}

func applyDeepSeekV4ClaudeThinkingSuffix(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) error {
	modelName := request.Model
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		modelName = info.UpstreamModelName
	}
	if model_setting.ShouldPreserveThinkingSuffix(modelName) || info != nil && model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) {
		return nil
	}
	baseModel, thinkingType, effort, ok := reasoning.ParseDeepSeekV4ThinkingSuffix(modelName)
	if !ok {
		return nil
	}
	request.Model = baseModel
	request.Thinking = &dto.Thinking{Type: thinkingType}
	if effort == "" {
		request.OutputConfig = nil
	} else {
		outputConfig, err := common.Marshal(map[string]string{
			"effort": effort,
		})
		if err != nil {
			return fmt.Errorf("error marshalling output_config: %w", err)
		}
		request.OutputConfig = outputConfig
	}
	if info != nil {
		if info.ChannelMeta != nil {
			info.UpstreamModelName = baseModel
		}
		info.SetReasoningEffort(effort)
	}
	return nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	applyDeepSeekV4ResponsesThinkingSuffix(info, &request)
	if err := backfillDeepSeekResponsesReasoningText(&request); err != nil {
		return nil, err
	}
	return request, nil
}

// DeepSeek rejects replayed tool-call turns whose reasoning echo is missing. The
// field carrying it differs per wire format — reasoning_text, content[].thinking,
// reasoning_content — but the requirement is the same one, so all three backfills
// share this placeholder. It acknowledges unavailable history; it does not
// recover the model's original reasoning.
const deepSeekReasoningPlaceholder = "reasoning unavailable"

// backfillDeepSeekClaudeThinkingBlocks gives every assistant turn that replays a
// tool call a thinking block. DeepSeek's Anthropic endpoint accepts a text-only
// assistant turn without one, but rejects a thinking-mode tool turn whose
// reasoning was not passed back. A thinking block is required to exist, not to
// carry text or a signature, so a placeholder satisfies the check.
func backfillDeepSeekClaudeThinkingBlocks(request *dto.ClaudeRequest) {
	if request == nil || request.Thinking != nil && request.Thinking.Type == "disabled" {
		return
	}
	for i := range request.Messages {
		message := &request.Messages[i]
		if message.Role != "assistant" {
			continue
		}
		blocks, ok := message.Content.([]any)
		if !ok {
			continue
		}
		hasToolUse, hasThinking := false, false
		for _, block := range blocks {
			entry, ok := block.(map[string]any)
			if !ok {
				continue
			}
			switch entry["type"] {
			case "tool_use":
				hasToolUse = true
			case "thinking", "redacted_thinking":
				hasThinking = true
			}
		}
		if hasToolUse && !hasThinking {
			message.Content = append([]any{map[string]any{
				"type":     "thinking",
				"thinking": deepSeekReasoningPlaceholder,
			}}, blocks...)
		}
	}
}

// backfillDeepSeekOpenAIReasoningContent is backfillDeepSeekClaudeThinkingBlocks
// on the chat completions wire. Only reasoning_content satisfies DeepSeek;
// a same-shaped reasoning field does not, so it is checked separately.
func backfillDeepSeekOpenAIReasoningContent(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
	if len(request.THINKING) > 0 {
		var thinking struct {
			Type string `json:"type"`
		}
		if err := common.Unmarshal(request.THINKING, &thinking); err == nil && thinking.Type == "disabled" {
			return
		}
	}
	for i := range request.Messages {
		message := &request.Messages[i]
		if message.Role != "assistant" || len(message.ToolCalls) == 0 {
			continue
		}
		if message.ReasoningContent != nil && strings.TrimSpace(*message.ReasoningContent) != "" {
			continue
		}
		placeholder := deepSeekReasoningPlaceholder
		message.ReasoningContent = &placeholder
	}
}

// DeepSeek requires reasoning_text for assistant turns when thinking/tool use is
// enabled. Mixed-provider histories may omit the entire reasoning item, not just
// its content. Keep consecutive assistant messages and parallel calls together;
// user messages and tool results start a new turn. A placeholder acknowledges
// unavailable history; it does not recover the model's original reasoning.
func backfillDeepSeekResponsesReasoningText(request *dto.OpenAIResponsesRequest) error {
	if request == nil || !bytes.HasPrefix(bytes.TrimSpace(request.Input), []byte("[")) {
		return nil
	}
	var items []json.RawMessage
	if err := common.Unmarshal(request.Input, &items); err != nil {
		return err
	}
	output := make([]json.RawMessage, 0, len(items))
	changed, hasReasoning := false, false
	for _, raw := range items {
		var probe struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Summary []struct {
				Text string `json:"text"`
			} `json:"summary"`
		}
		if err := common.Unmarshal(raw, &probe); err != nil {
			return err
		}
		if probe.Type != "reasoning" {
			if probe.Role == "assistant" || probe.Type == "function_call" {
				if !hasReasoning {
					placeholder, err := common.Marshal(map[string]any{
						"type":    "reasoning",
						"summary": []any{},
						"content": []map[string]string{{"type": "reasoning_text", "text": deepSeekReasoningPlaceholder}},
					})
					if err != nil {
						return err
					}
					output = append(output, placeholder)
					changed = true
				}
				hasReasoning = true
			} else {
				hasReasoning = false
			}
			output = append(output, raw)
			continue
		}
		hasReasoning = true
		var entries []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if len(probe.Content) > 0 {
			if err := common.Unmarshal(probe.Content, &entries); err != nil {
				return err
			}
		}
		hasText := false
		for _, entry := range entries {
			if entry.Type == "reasoning_text" && entry.Text != "" {
				hasText = true
				break
			}
		}
		if hasText {
			output = append(output, raw)
			continue
		}
		var builder strings.Builder
		for _, summary := range probe.Summary {
			builder.WriteString(summary.Text)
		}
		text := strings.TrimSpace(builder.String())
		if text == "" {
			text = deepSeekReasoningPlaceholder
		}
		var item map[string]json.RawMessage
		if err := common.Unmarshal(raw, &item); err != nil {
			return err
		}
		content, err := common.Marshal([]map[string]string{{"type": "reasoning_text", "text": text}})
		if err != nil {
			return err
		}
		item["content"] = content
		encoded, err := common.Marshal(item)
		if err != nil {
			return err
		}
		output = append(output, encoded)
		changed = true
	}
	if !changed {
		return nil
	}
	encoded, err := common.Marshal(output)
	if err != nil {
		return err
	}
	request.Input = encoded
	return nil
}

func applyDeepSeekV4ResponsesThinkingSuffix(info *relaycommon.RelayInfo, request *dto.OpenAIResponsesRequest) {
	modelName := request.Model
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		modelName = info.UpstreamModelName
	}
	if model_setting.ShouldPreserveThinkingSuffix(modelName) || info != nil && model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) {
		return
	}
	baseModel, thinkingType, effort, ok := reasoning.ParseDeepSeekV4ThinkingSuffix(modelName)
	if ok {
		if thinkingType == "disabled" {
			effort = "none"
		}
		request.Model = baseModel
		if request.Reasoning == nil {
			request.Reasoning = &dto.Reasoning{}
		}
		request.Reasoning.Effort = effort
		if info != nil && info.ChannelMeta != nil {
			info.UpstreamModelName = baseModel
		}
	}
	if info != nil && request.Reasoning != nil {
		info.SetReasoningEffort(request.Reasoning.Effort)
	}
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
