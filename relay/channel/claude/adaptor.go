package claude

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	// Strip fields that Bedrock proxies don't support.
	// - context_management: Bedrock returns empty responses
	// - output_config: Bedrock rejects effort for some models (e.g. haiku)
	// Native Anthropic endpoints ignore unknown fields, so stripping is harmless.
	request.ContextManagement = nil
	request.OutputConfig = nil
	return request, nil
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
	requestURL := fmt.Sprintf("%s/v1/messages", info.ChannelBaseUrl)
	if !shouldAppendClaudeBetaQuery(info) {
		return requestURL, nil
	}

	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return "", err
	}
	query := parsedURL.Query()
	query.Set("beta", "true")
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}

func shouldAppendClaudeBetaQuery(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	if info.IsClaudeBetaQuery {
		return true
	}
	if info.ChannelOtherSettings.ClaudeBetaQuery {
		return true
	}
	return false
}

// bedrockSupportedBetaFlags lists anthropic-beta tokens that AWS Bedrock
// is known to accept. When a channel has StripUnsupportedBeta enabled,
// only these flags pass through; everything else is stripped so new
// Claude CLI beta flags don't cause Bedrock 400 errors.
var bedrockSupportedBetaFlags = map[string]bool{
	"prompt-caching-2024-07-31":     true,
	"output-128k-2025-02-19":        true,
	"token-efficient-tools-2025-02-19": true,
	"interleaved-thinking-2025-05-14": true,
	"code-execution-2025-05-22":     true,
	"files-api-2025-04-14":          true,
	"pdfs-2024-09-25":               true,
	"max-tokens-3-5-sonnet-2024-07-15": true,
}

// filterAnthropicBetaByWhitelist keeps only beta flags present in the allow list.
func filterAnthropicBetaByWhitelist(beta string, allowed map[string]bool) string {
	if beta == "" {
		return ""
	}
	parts := strings.Split(beta, ",")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if allowed[p] {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, ",")
}


func CommonClaudeHeadersOperation(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) {
	// common headers operation
	anthropicBeta := c.Request.Header.Get("anthropic-beta")
	if anthropicBeta != "" {
		var filtered string
		if info != nil && info.ChannelOtherSettings.StripUnsupportedBeta {
			// Whitelist mode: only keep Bedrock-supported flags.
			// New Claude CLI beta flags are stripped by default.
			filtered = filterAnthropicBetaByWhitelist(anthropicBeta, bedrockSupportedBetaFlags)
		} else {
			// Pass-through mode: no filtering for non-Bedrock channels.
			filtered = anthropicBeta
		}
		// Overwrite the original request header so any downstream code path
		// (retries, pass-through, header overrides) sees the filtered value.
		c.Request.Header.Set("anthropic-beta", filtered)
		if filtered != "" {
			req.Set("anthropic-beta", filtered)
		} else {
			req.Del("anthropic-beta")
		}
	}
	model_setting.GetClaudeSettings().WriteHeaders(info.OriginModelName, req)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("x-api-key", info.ApiKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Set("anthropic-version", anthropicVersion)
	CommonClaudeHeadersOperation(c, req, info)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	result, err := relayconvert.ConvertRequest(c, info, types.RelayFormatClaude, request)
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	info.FinalRequestRelayFormat = types.RelayFormatClaude
	if info.IsStream {
		return ClaudeStreamHandler(c, resp, info)
	} else {
		return ClaudeHandler(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
