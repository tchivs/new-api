package plugins_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHailuoResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "hailuo",
		model:     "MiniMax-Hailuo-2.3",
		requestBody: map[string]any{
			"model": "MiniMax-Hailuo-2.3",
			"input": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "ocean at sunset"},
				map[string]any{"type": "input_image", "image_url": "https://cdn.example/frame.png"},
			}}},
			"seconds": 10,
			"size":    "1920x1080",
		},
		wantAction: "image_to_video",
		wantRequest: map[string]any{
			"model":    "MiniMax-Hailuo-2.3",
			"prompt":   "ocean at sunset",
			"images":   []any{"https://cdn.example/frame.png"},
			"duration": float64(10),
			"size":     "1920x1080",
			"metadata": map[string]any{"first_frame_image": "https://cdn.example/frame.png"},
		},
		wantUsageKeys:  []string{"input_images", "input_video_seconds", "resolution", "seconds"},
		wantVendorName: "hailuo",
	})
}

// Every model selects only the dimensions it can actually bill, while
// submission still reports the union so prices saved against the previous
// schema keep evaluating.
func TestHailuoUsageProfilesPerModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	families := []struct {
		models      []string
		fields      []string
		resolutions []string
	}{
		{
			models:      []string{"MiniMax-H3", "minimax/minimax-h3"},
			fields:      []string{"input_images", "input_video_seconds", "resolution", "seconds"},
			resolutions: []string{"768P", "2K"},
		},
		{
			models:      []string{"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"768P", "1080P"},
		},
		{
			models:      []string{"MiniMax-Hailuo-02"},
			fields:      []string{"resolution", "seconds"},
			resolutions: []string{"512P", "768P", "1080P"},
		},
		{
			models: []string{"T2V-01-Director", "T2V-01", "I2V-01-Director", "I2V-01-live", "I2V-01", "S2V-01"},
			fields: []string{"seconds"},
		},
	}
	profiled := make([]string, 0, len(plugin.Meta.Models))
	for _, family := range families {
		for _, name := range family.models {
			profiled = append(profiled, name)
			t.Run(name, func(t *testing.T) {
				schema, examples := plugin.Meta.UsageForModel(name)
				assert.Equal(t, family.fields, slices.Sorted(maps.Keys(schema)))
				assert.Equal(t, family.resolutions, schema["resolution"].Enum)
				assert.NotEmpty(t, examples)
			})
		}
	}
	assert.ElementsMatch(t, plugin.Meta.Models, profiled, "every declared model must select a usage profile")

	t.Run("submission keeps facts outside the selected profile billable", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example", UpstreamModelName: "MiniMax-Hailuo-2.3"},
			OriginModelName: "MiniMax-Hailuo-2.3",
			TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
		}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "duration": 10, "resolution": "768P"})
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))

		facts, err := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}, facts)
	})
}

// MiniMax serves v1 videos only through the signed download_url of
// /v1/files/retrieve; that link is signed for GET, so HEAD clients get GET too.
func TestHailuoArtifactContentProxy(t *testing.T) {
	service.InitHttpClient()
	const signedURL = "https://bucket.oss-cn-wulanchabu.aliyuncs.com/video.mp4?Expires=1&OSSAccessKeyId=key&Signature=sig"
	retrieveBodies := map[string]string{
		"file/with space": `{"file":{"file_id":1,"download_url":"` + signedURL + `"},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"login":           `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`,
		"missing":         `{"file":{"file_id":3},"base_resp":{"status_code":0,"status_msg":"success"}}`,
		"html":            `<html>gateway error</html>`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/files/retrieve", r.URL.Path)
		assert.Equal(t, "Bearer test-ak", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(retrieveBodies[r.URL.Query().Get("file_id")]))
	}))
	defer upstream.Close()
	adaptor := taskplugin.New(loadHailuoPlugin(t))
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: upstream.URL},
	})

	for _, tc := range []struct {
		fileID  string
		wantErr string
	}{
		{fileID: "file/with space"},
		{fileID: "login", wantErr: "MiniMax file retrieve failed: login fail"},
		{fileID: "missing", wantErr: "artifact_not_found"},
		{fileID: "html", wantErr: "MiniMax file retrieve returned an unexpected response: HTTP 200"},
	} {
		t.Run(tc.fileID, func(t *testing.T) {
			data, err := common.Marshal(map[string]any{"file_id": tc.fileID})
			require.NoError(t, err)
			task := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Data: data}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)

			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, &channel.TaskContentRequest{URL: signedURL, Method: http.MethodGet, Credentialless: true}, descriptor)
		})
	}
}

func loadHailuoPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	return plugin
}

func callHailuoHook(t *testing.T, plugin *jsplugin.LoadedPlugin, hook string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.Call(t.Context(), hook, args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	return decoded
}

func hailuoH3SubmitContext(requestBody map[string]any) map[string]any {
	return map[string]any{
		"requestBody":   requestBody,
		"model":         "MiniMax-H3",
		"upstreamModel": "MiniMax-H3",
		"baseUrl":       "https://api.minimax.example",
		"apiKey":        "test-ak",
	}
}

// MiniMax-H3 submits to /v2/video_generation with a multimodal content array
// instead of the flat /v1 frame fields.
func TestHailuoH3BuildSubmitRequest(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		request    map[string]any
		wantBody   string
		wantAction string
	}{
		{
			name:       "text to video defaults duration ratio and resolution",
			request:    map[string]any{"prompt": "a boy playing basketball"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"a boy playing basketball"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:       "2K resolution from size",
			request:    map[string]any{"prompt": "p", "duration": 15, "size": "2K"},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"2K","duration":15,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name:    "first and last frame from metadata",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"first_frame_image": "first.png", "last_frame_image": "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name:    "frames from the images array",
			request: map[string]any{"prompt": "p", "images": []any{"first.png", "last.png"}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"first_frame","image_url":{"url":"first.png"}},
				{"type":"image_url","role":"last_frame","image_url":{"url":"last.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "reference video and audio",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{
				"reference_video": "ref.mp4",
				"reference_audio": []any{"a.mp3", "b.mp3"},
			}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"video_url","role":"reference_video","video_url":{"url":"ref.mp4"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"a.mp3"}},
				{"type":"audio_url","role":"reference_audio","audio_url":{"url":"b.mp3"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough prepends the prompt when no text item exists",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "img.png"}},
			}}},
			wantBody: `{"model":"MiniMax-H3","content":[
				{"type":"text","text":"p"},
				{"type":"image_url","role":"reference_image","image_url":{"url":"img.png"}}],
				"resolution":"768P","duration":5,"ratio":"adaptive"}`,
			wantAction: "image_to_video",
		},
		{
			name: "content passthrough keeps an existing text item",
			request: map[string]any{"prompt": "ignored", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "kept"},
			}}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"kept"}],"resolution":"768P","duration":5,"ratio":"16:9"}`,
			wantAction: "text_to_video",
		},
		{
			name: "explicit ratio callback and watermark",
			request: map[string]any{"prompt": "p", "duration": 4, "metadata": map[string]any{
				"ratio":          "9:16",
				"callback_url":   "https://example.com/cb",
				"aigc_watermark": true,
			}},
			wantBody:   `{"model":"MiniMax-H3","content":[{"type":"text","text":"p"}],"resolution":"768P","duration":4,"ratio":"9:16","callback_url":"https://example.com/cb","aigc_watermark":true}`,
			wantAction: "text_to_video",
		},
	}
	for _, testCase := range testCases {
		for _, upstreamModel := range []string{"MiniMax-H3", "minimax/minimax-h3"} {
			if upstreamModel == "minimax/minimax-h3" && testCase.wantAction == "image_to_video" {
				continue
			}
			t.Run(testCase.name+"/"+upstreamModel, func(t *testing.T) {
				ctx := hailuoH3SubmitContext(testCase.request)
				ctx["upstreamModel"] = upstreamModel
				descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)
				path := "/v2/video_generation"
				if upstreamModel == "minimax/minimax-h3" {
					path = "/v1/videos"
				}
				assert.Equal(t, "https://api.minimax.example"+path, descriptor["url"])
				assert.Equal(t, "POST", descriptor["method"])
				assert.Equal(t, testCase.wantAction, descriptor["action"])
				assert.NotContains(t, descriptor, "model", "mapped client identity stays host-owned")
				body, err := common.Marshal(descriptor["body"])
				require.NoError(t, err)
				assert.JSONEq(t, strings.ReplaceAll(testCase.wantBody, "MiniMax-H3", upstreamModel), string(body))
			})
		}
	}
	t.Run("Zenmux rejects input media until measured usage is available", func(t *testing.T) {
		for _, mediaType := range []string{"image_url", "video_url", "audio_url"} {
			ctx := hailuoH3SubmitContext(map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": mediaType, mediaType: map[string]any{"url": "https://cdn.example/input"}},
			}}})
			ctx["upstreamModel"] = "minimax/minimax-h3"
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.ErrorContains(t, err, "currently supports text-to-video only")
		}
	})
}

// Every MiniMax-H3 request bound is rejected before the upstream call, so an
// out-of-range duration can never reach quota calculation as a billing fact.
func TestHailuoH3RejectsOutOfContractRequests(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	tenReferenceImages := make([]any, 0, 10)
	for range 10 {
		tenReferenceImages = append(tenReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "u"},
		})
	}
	testCases := []struct {
		name    string
		request map[string]any
		wantErr string
	}{
		{"duration below the minimum", map[string]any{"prompt": "p", "duration": 3}, "duration must be an integer between 4 and 15"},
		{"duration above the maximum", map[string]any{"prompt": "p", "duration": 16}, "duration must be an integer between 4 and 15"},
		{"fractional duration", map[string]any{"prompt": "p", "duration": 5.5}, "duration must be an integer between 4 and 15"},
		{"unsupported resolution", map[string]any{"prompt": "p", "size": "1080P"}, "resolution must be 768P or 2K"},
		{"unknown ratio", map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "16:10"}}, "ratio must be one of"},
		{"adaptive ratio without a visual input", map[string]any{"prompt": "p", "metadata": map[string]any{"ratio": "adaptive"}}, "ratio adaptive requires an image or video input"},
		{"too many frame images", map[string]any{"prompt": "p", "images": []any{"a.png", "b.png", "c.png"}}, "at most 2 frame images"},
		{"media without text", map[string]any{"images": []any{"a.png"}}, "requires a non-empty text item"},
		{"content is not an array", map[string]any{"prompt": "p", "metadata": map[string]any{"content": "nope"}}, "metadata.content must be an array"},
		{
			name: "multiple first frame roles",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "u2"}},
			}}},
			wantErr: "at most one first_frame image",
		},
		{
			name: "passthrough mixes frame and reference media",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "frame"}},
				map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "reference"}},
			}}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "assembled content mixes frame and reference media",
			request: map[string]any{"prompt": "p", "images": []any{"frame"}, "metadata": map[string]any{
				"reference_video": "reference.mp4",
			}},
			wantErr: "cannot mix frame images with reference media",
		},
		{
			name: "too many reference videos",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": []any{
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u1"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u2"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u3"}},
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "u4"}},
			}}},
			wantErr: "at most 3 reference videos",
		},
		{
			name:    "too many reference images",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"content": tenReferenceImages}},
			wantErr: "at most 9 reference images",
		},
		{
			name:    "too many reference audios",
			request: map[string]any{"prompt": "p", "metadata": map[string]any{"reference_audio": []any{"a.mp3", "b.mp3", "c.mp3", "d.mp3"}}},
			wantErr: "at most 3 reference audios",
		},
		{"empty input", map[string]any{"prompt": "  "}, "requires a prompt or a media input"},
	}
	for _, testCase := range testCases {
		for _, upstreamModel := range []string{"MiniMax-H3", "minimax/minimax-h3"} {
			t.Run(testCase.name+"/"+upstreamModel, func(t *testing.T) {
				ctx := hailuoH3SubmitContext(testCase.request)
				ctx["upstreamModel"] = upstreamModel
				_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.ErrorContains(t, err, testCase.wantErr)
			})
		}
	}
}

// The /v1 models keep the flat request shape and endpoint.
func TestHailuoLegacySubmitRequestUnchanged(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, name := range []string{"MiniMax-Hailuo-2.3", "MiniMax-H3-Max", "minimax/minimax-h3-max"} {
		t.Run(name, func(t *testing.T) {
			ctx := hailuoH3SubmitContext(map[string]any{"prompt": "p", "duration": 10, "size": "768P"})
			ctx["model"] = name
			ctx["upstreamModel"] = name
			descriptor := callHailuoHook(t, plugin, "buildSubmitRequest", ctx)
			assert.Equal(t, "https://api.minimax.example/v1/video_generation", descriptor["url"])
			body, err := common.Marshal(descriptor["body"])
			require.NoError(t, err)
			assert.JSONEq(t, `{"model":"`+name+`","prompt":"p","duration":10,"resolution":"768P"}`, string(body))
		})
	}
}

func TestHailuoQueryRequestEndpointByModel(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		ctx  map[string]any
		want string
	}{
		{
			name: "H3 uses the v2 path parameter",
			ctx:  map[string]any{"taskId": "task/1", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/task%2F1",
		},
		{
			name: "an unmapped task falls back to the origin model",
			ctx:  map[string]any{"taskId": "t1", "model": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
		{
			name: "legacy models keep the v1 query parameter",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "MiniMax-Hailuo-2.3"},
			want: "https://api.minimax.example/v1/query/video_generation?task_id=t1",
		},
		{
			name: "a channel-mapped alias resolves through the upstream model",
			ctx:  map[string]any{"taskId": "t1", "model": "h3", "upstreamModel": "MiniMax-H3"},
			want: "https://api.minimax.example/v2/query/video_generation/t1",
		},
		{
			name: "Zenmux mapped H3 uses native videos",
			ctx:  map[string]any{"taskId": "opaque/1", "model": "MiniMax-H3", "upstreamModel": "minimax/minimax-h3"},
			want: "https://api.minimax.example/v1/videos/opaque%2F1",
		},
		{
			name: "Zenmux unmapped machine name",
			ctx:  map[string]any{"taskId": "opaque", "model": "minimax/minimax-h3"},
			want: "https://api.minimax.example/v1/videos/opaque",
		},
		{
			name: "H3 Max is not ordinary H3",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "minimax/minimax-h3-max"},
			want: "https://api.minimax.example/v1/query/video_generation?task_id=t1",
		},
		{
			name: "a New API gateway serves H3 on the prefixed v2 route",
			ctx:  map[string]any{"taskId": "t1", "upstreamModel": "MiniMax-H3", "upstream": map[string]any{"kind": "new_api"}},
			want: "https://api.minimax.example/hailuo/v2/query/video_generation/t1",
		},
		{
			name: "a New API gateway serves Zenmux H3 through the native H3 route",
			ctx:  map[string]any{"taskId": "opaque/1", "upstreamModel": "minimax/minimax-h3", "upstream": map[string]any{"kind": "new_api"}},
			want: "https://api.minimax.example/hailuo/v2/query/video_generation/opaque%2F1",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.ctx["baseUrl"] = "https://api.minimax.example"
			testCase.ctx["apiKey"] = "test-ak"
			descriptor := callHailuoHook(t, plugin, "buildQueryRequest", testCase.ctx)
			assert.Equal(t, testCase.want, descriptor["url"])
			assert.Equal(t, "GET", descriptor["method"])
		})
	}
}

func TestHailuoParseTaskResult(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name       string
		body       string
		wantStatus string
		wantURL    string
		wantReason string
	}{
		{"H3 queued", `{"task":{"id":"1","status":"queued"}}`, "QUEUED", "", ""},
		{"H3 running", `{"task":{"id":"1","status":"running"}}`, "IN_PROGRESS", "", ""},
		{"H3 succeeded", `{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`, "SUCCESS", "https://cdn.example/h3.mp4", ""},
		{"H3 succeeded without video", `{"task":{"id":"1","status":"succeeded"}}`, "UNKNOWN", "", "missing video URL"},
		{"H3 malformed video", `{"task":{"id":"1","status":"succeeded","content":{"url":{"url":"not a video URL"}}}}`, "UNKNOWN", "", "missing video URL"},
		{"Zenmux queued", `{"id":"opaque","status":"queued","model":"minimax/minimax-h3"}`, "QUEUED", "", ""},
		{"Zenmux running", `{"id":"opaque","status":"running","model":"minimax/minimax-h3"}`, "IN_PROGRESS", "", ""},
		{"Zenmux succeeded", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","content":{"video_url":"https://video-product.cdn.minimax.io/h3.mp4?sig=1"}}`, "SUCCESS", "https://video-product.cdn.minimax.io/h3.mp4?sig=1", ""},
		{"Zenmux missing video", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3"}`, "UNKNOWN", "", "missing video URL"},
		{"Zenmux blank video", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","content":{"video_url":" "}}`, "UNKNOWN", "", "missing video URL"},
		{"Zenmux failed", `{"id":"opaque","status":"failed","model":"minimax/minimax-h3","error":{"message":"provider failed"}}`, "FAILURE", "", "provider failed"},
		{"Zenmux cancelled", `{"id":"opaque","status":"cancelled","model":"minimax/minimax-h3"}`, "FAILURE", "", "task cancelled"},
		{"Zenmux unknown", `{"id":"opaque","status":"weird","model":"minimax/minimax-h3"}`, "UNKNOWN", "", "unrecognized status: weird"},
		{"Zenmux missing status", `{"id":"opaque","model":"minimax/minimax-h3"}`, "UNKNOWN", "", "unrecognized status: "},
		{"H3 Max top-level is not ordinary H3", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3-max","content":{"video_url":"https://cdn.example/max.mp4"}}`, "UNKNOWN", "", "unrecognized status: succeeded"},
		{"H3 failed", `{"task":{"id":"1","status":"failed","error":{"code":"1026","message":"sensitive content"}}}`, "FAILURE", "", "sensitive content"},
		{"H3 cancelled", `{"task":{"id":"1","status":"cancelled"}}`, "FAILURE", "", "task cancelled"},
		{"H3 permanent query error", `{"type":"error","error":{"type":"authorized_error","message":"login failed","http_code":"401"}}`, "FAILURE", "", "login failed"},
		{"legacy success", `{"task_id":"1","status":"Success","file_id":"f1","base_resp":{"status_code":0}}`, "SUCCESS", "", ""},
		{"legacy processing", `{"task_id":"1","status":"Processing","base_resp":{"status_code":0}}`, "IN_PROGRESS", "", ""},
		{"H3 unrecognized", `{"task":{"id":"1","status":"weird"}}`, "UNKNOWN", "", "unrecognized status: weird"},
		{"legacy unrecognized", `{"task_id":"1","status":"Weird","base_resp":{"status_code":0}}`, "UNKNOWN", "", "unrecognized status: Weird"},
		{"legacy base_resp failure", `{"task_id":"1","status":"Success","base_resp":{"status_code":1001,"status_msg":"upstream down"}}`, "FAILURE", "", "upstream down"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			result := callHailuoHook(t, plugin, "parseTaskResult", map[string]any{}, body)
			assert.Equal(t, testCase.wantStatus, result["status"])
			assert.Equal(t, testCase.wantURL, common.Interface2String(result["url"]))
			assert.Equal(t, testCase.wantReason, common.Interface2String(result["reason"]))
		})
	}
	t.Run("mapped Zenmux top-level result without echoed model", func(t *testing.T) {
		ctx := map[string]any{"model": "MiniMax-H3", "upstreamModel": "minimax/minimax-h3"}
		result := callHailuoHook(t, plugin, "parseTaskResult", ctx,
			map[string]any{"id": "opaque", "status": "succeeded", "content": map[string]any{"video_url": "https://video-product.cdn.minimax.io/h3.mp4"}})
		assert.Equal(t, "SUCCESS", result["status"])
		result = callHailuoHook(t, plugin, "parseTaskResult", ctx, map[string]any{"status": "Success"})
		assert.Equal(t, "UNKNOWN", result["status"], "legacy success without a video must not deliver an H3 success")
		result = callHailuoHook(t, plugin, "parseTaskResult", ctx,
			map[string]any{"status": "succeeded", "base_resp": map[string]any{"status_code": 1001, "status_msg": "upstream down"}})
		assert.Equal(t, "FAILURE", result["status"])
	})
	t.Run("H3 retryable query error", func(t *testing.T) {
		var body any
		require.NoError(t, common.UnmarshalJsonStr(
			`{"type":"error","error":{"type":"rate_limit_error","message":"retry later","http_code":"429"}}`, &body))
		_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
		require.ErrorContains(t, err, "retry later")
	})
}

func TestHailuoExtractUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	nineReferenceImages := make([]any, 0, 9)
	for range 9 {
		nineReferenceImages = append(nineReferenceImages, map[string]any{
			"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "image"},
		})
	}
	testCases := []struct {
		name    string
		model   string
		request map[string]any
		want    map[string]any
	}{
		{"H3 defaults", "MiniMax-H3", map[string]any{"prompt": "p"}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"Zenmux reserves requested quantities", "minimax/minimax-h3", map[string]any{"prompt": "p", "duration": 4}, map[string]any{
			"seconds": float64(4), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 2K", "MiniMax-H3", map[string]any{"prompt": "p", "duration": 12, "size": "2K"}, map[string]any{
			"seconds": float64(12), "resolution": "2K", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
		{"H3 reference images", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{"content": nineReferenceImages}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(9), "input_video_seconds": float64(0),
		}},
		{"H3 reference video reserves total duration limit", "MiniMax-H3", map[string]any{"prompt": "p", "metadata": map[string]any{
			"reference_video": []any{"one.mp4", "two.mp4", "three.mp4"},
		}}, map[string]any{
			"seconds": float64(5), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(15),
		}},
		{"legacy model", "MiniMax-Hailuo-2.3", map[string]any{"prompt": "p", "duration": 10}, map[string]any{
			"seconds": float64(10), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0),
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := hailuoH3SubmitContext(testCase.request)
			ctx["model"] = testCase.model
			ctx["upstreamModel"] = testCase.model
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsage", ctx))
		})
	}
}

func TestHailuoH3CompletionUsageFacts(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	testCases := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "actual usage replaces submission estimates",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
			want: map[string]any{"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5)},
		},
		{
			name: "zero actual usage is retained for settlement",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":4,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"seconds": float64(4), "resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "zero output cannot erase the submission reservation",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P","usage":{"output_seconds":0,"input_seconds":0,"input_image_count":0}}}`,
			want: map[string]any{"resolution": "768P", "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "missing usage leaves submission estimates untouched",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"768P"}}`,
			want: map[string]any{"resolution": "768P"},
		},
		{
			name: "out of contract usage cannot become a billing multiplier",
			body: `{"task":{"id":"1","status":"succeeded","resolution":"2K","usage":{"output_seconds":16,"input_seconds":16,"input_image_count":10}}}`,
			want: map[string]any{"resolution": "2K"},
		},
		{
			name: "Zenmux missing usage does not infer duration or resolution",
			body: `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","duration":12,"video_width":2048,"video_height":1152,"content":{"video_url":"https://video-product.cdn.minimax.io/h3.mp4"}}`,
		},
		{
			name: "Zenmux bounded measured usage",
			body: `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","usage":{"output_seconds":4,"input_seconds":0,"input_image_count":0}}`,
			want: map[string]any{"seconds": float64(4), "input_images": float64(0), "input_video_seconds": float64(0)},
		},
		{
			name: "Zenmux out of contract usage preserves frozen reservation",
			body: `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","usage":{"output_seconds":16,"input_seconds":-1,"input_image_count":1.5}}`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var body any
			require.NoError(t, common.UnmarshalJsonStr(testCase.body, &body))
			assert.Equal(t, testCase.want, callHailuoHook(t, plugin, "extractUsageOnComplete", nil, nil, body))
		})
	}

	t.Run("polling adaptor carries actual facts into task settlement", func(t *testing.T) {
		adaptor := taskplugin.New(plugin)
		result, err := adaptor.ParseTaskResult(&model.Task{}, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, []byte(
			`{"task":{"id":"1","status":"succeeded","resolution":"2K","content":{"url":"https://cdn.example/h3.mp4"},"usage":{"output_seconds":5,"input_seconds":7.5,"input_image_count":6}}}`,
		))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"seconds": float64(5), "resolution": "2K", "input_images": float64(6), "input_video_seconds": float64(7.5),
		}, result.UsageFacts)
	})
}

// The /v2 result is a public CDN URL, so its artifact is proxied without
// channel credentials instead of through the /v1 file download endpoint.
func TestHailuoH3ArtifactContentProxy(t *testing.T) {
	source, err := builtinplugins.Source("hailuo")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "hailuo"})
	require.NoError(t, err)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-ak", ChannelBaseUrl: "https://api.minimax.example"},
	})
	for _, tc := range []struct {
		name string
		body string
		url  string
	}{
		{"official", `{"task":{"id":"1","status":"succeeded","content":{"url":"https://cdn.example/h3.mp4"}}}`, "https://cdn.example/h3.mp4"},
		{"Zenmux", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3","content":{"video_url":"https://video-product.cdn.minimax.io/h3.mp4?sig=1"}}`, "https://video-product.cdn.minimax.io/h3.mp4?sig=1"},
		{"missing video", `{"id":"opaque","status":"succeeded","model":"minimax/minimax-h3"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{TaskID: "task-public", Status: model.TaskStatusSuccess, Data: []byte(tc.body)}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			if tc.url == "" {
				assert.Empty(t, artifacts)
				_, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
				require.ErrorContains(t, err, "artifact_not_found")
				return
			}
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: method})
				require.NoError(t, err)
				assert.Equal(t, &channel.TaskContentRequest{URL: tc.url, Method: method, Credentialless: true}, descriptor)
			}
			pending := &model.Task{TaskID: "task-public", Status: model.TaskStatusInProgress, Data: task.Data}
			pendingArtifacts, err := adaptor.ListArtifacts(pending)
			require.NoError(t, err)
			assert.Empty(t, pendingArtifacts)
		})
	}
}

// The /v2 create response carries only task_id, without the /v1 base_resp envelope.
func TestHailuoParseSubmitResponse(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	t.Run("H3 create has no envelope", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"task_id": "h3-1"}})
		assert.Equal(t, "h3-1", parsed["taskId"])
	})
	t.Run("Zenmux mapped create returns opaque id", func(t *testing.T) {
		body := map[string]any{"id": "opaque", "status": "queued", "model": "minimax/minimax-h3"}
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse",
			map[string]any{"model": "MiniMax-H3", "upstreamModel": "minimax/minimax-h3"}, map[string]any{"body": body})
		assert.Equal(t, "opaque", parsed["taskId"])
		assert.Equal(t, body, parsed["taskData"])
	})
	t.Run("Zenmux missing id is rejected", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "minimax/minimax-h3"},
			map[string]any{"body": map[string]any{"status": "queued"}})
		require.ErrorContains(t, err, "missing id")
	})
	t.Run("H3 rejection reports the envelope message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 2013, "status_msg": "invalid params"}}})
		require.ErrorContains(t, err, "invalid params")
	})
	t.Run("H3 rejection reports the v2 error message", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-H3"},
			map[string]any{"body": map[string]any{"type": "error", "error": map[string]any{
				"type": "bad_request_error", "message": "content requires text", "http_code": "400",
			}}})
		require.ErrorContains(t, err, "content requires text")
	})
	t.Run("legacy create", func(t *testing.T) {
		parsed := callHailuoHook(t, plugin, "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"task_id": "v1-1", "base_resp": map[string]any{"status_code": 0}}})
		assert.Equal(t, "v1-1", parsed["taskId"])
	})
	t.Run("legacy response without an envelope is rejected", func(t *testing.T) {
		ctx := map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"}
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx,
			map[string]any{"body": map[string]any{"task_id": "v1-1"}})
		require.ErrorContains(t, err, "hailuo submit failed")
	})
	t.Run("legacy error", func(t *testing.T) {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{"upstreamModel": "MiniMax-Hailuo-2.3"},
			map[string]any{"body": map[string]any{"base_resp": map[string]any{"status_code": 1026, "status_msg": "sensitive"}}})
		require.ErrorContains(t, err, "sensitive")
	})
}

// Without an H3 exemption the shared /v1 combo table would reject every H3
// duration on the OpenAI Video path before the request is ever built.
func TestHailuoH3PassesOpenAIVideoDecode(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	for _, upstreamModel := range []string{"MiniMax-H3", "minimax/minimax-h3"} {
		t.Run(upstreamModel, func(t *testing.T) {
			value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"},
				map[string]any{
					"body":          map[string]any{"kind": "json", "value": map[string]any{"model": "MiniMax-H3", "prompt": "p", "seconds": 12, "size": "2K"}},
					"model":         "MiniMax-H3",
					"upstreamModel": upstreamModel,
				})
			require.NoError(t, err)
			encoded, err := common.Marshal(value)
			require.NoError(t, err)
			var intent map[string]any
			require.NoError(t, common.Unmarshal(encoded, &intent))
			assert.Equal(t, "submit", intent["kind"])
			assert.Equal(t, "MiniMax-H3", intent["model"])
			requestBody, ok := intent["requestBody"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, float64(12), requestBody["duration"])
		})
	}
}

// A New API channel reaches this plugin on another gateway. The downstream
// driver addresses the gateway's /hailuo routes, whose decoders give the
// gateway's driver the same request and whose presenters give the downstream
// driver the same task state, so MiniMax receives one body and both gateways
// settle on the same facts.
func TestHailuoThroughNewAPIGateway(t *testing.T) {
	plugin := loadHailuoPlugin(t)
	call := func(export string, members []string, args ...any) map[string]any {
		t.Helper()
		value, err := plugin.Engine.CallPath(t.Context(), export, members, args...)
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(encoded, &decoded))
		return decoded
	}

	for _, tc := range []struct {
		name        string
		model       string
		requestBody map[string]any
		route       string
		decoder     string
		vendorPath  string
	}{
		{
			name:  "v1 image to video",
			model: "MiniMax-Hailuo-02",
			requestBody: map[string]any{"model": "MiniMax-Hailuo-02", "prompt": "p", "duration": 10, "size": "512P", "metadata": map[string]any{
				"first_frame_image": "https://cdn.example/first.png", "prompt_optimizer": false,
			}},
			route:      "/hailuo/v1/video_generation",
			decoder:    "createTask",
			vendorPath: "/v1/video_generation",
		},
		{
			name:  "H3 reference video",
			model: "MiniMax-H3",
			requestBody: map[string]any{"model": "MiniMax-H3", "prompt": "p", "duration": 8, "size": "2K", "metadata": map[string]any{
				"reference_video": "https://cdn.example/ref.mp4", "ratio": "9:16", "aigc_watermark": true,
			}},
			route:      "/hailuo/v2/video_generation",
			decoder:    "createH3Task",
			vendorPath: "/v2/video_generation",
		},
		{
			name:        "Zenmux H3 text to video",
			model:       "minimax/minimax-h3",
			requestBody: map[string]any{"model": "minimax/minimax-h3", "prompt": "p", "duration": 5, "size": "2K"},
			route:       "/hailuo/v2/video_generation",
			decoder:     "createH3Task",
			vendorPath:  "/v1/videos",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downstream := map[string]any{"requestBody": tc.requestBody, "model": tc.model, "upstreamModel": tc.model,
				"baseUrl": "https://gateway.example", "apiKey": "sk-gateway", "upstream": map[string]any{"kind": "new_api"}}
			sent := call("buildSubmitRequest", nil, downstream)
			assert.Equal(t, "https://gateway.example"+tc.route, sent["url"])

			intent := call("native", []string{tc.decoder}, map[string]any{"body": map[string]any{"kind": "json", "value": sent["body"]}})
			assert.Equal(t, tc.model, intent["model"])
			assert.Equal(t, sent["action"], intent["action"])
			upstream := map[string]any{"requestBody": intent["requestBody"], "model": tc.model, "upstreamModel": tc.model,
				"baseUrl": "https://api.minimax.example", "apiKey": "vendor-key", "upstream": map[string]any{"kind": "vendor"}}
			forwarded := call("buildSubmitRequest", nil, upstream)
			assert.Equal(t, "https://api.minimax.example"+tc.vendorPath, forwarded["url"])
			assert.Equal(t, sent["body"], forwarded["body"])
			assert.Equal(t, call("extractUsage", nil, downstream), call("extractUsage", nil, upstream))
		})
	}

	t.Run("create responses", func(t *testing.T) {
		for model, data := range map[string]map[string]any{
			"MiniMax-Hailuo-02":  {"task_id": "task_pub", "base_resp": map[string]any{"status_code": 0, "status_msg": "success"}},
			"MiniMax-H3":         {"task_id": "task_pub"},
			"minimax/minimax-h3": {"task_id": "task_pub"},
		} {
			created := call("native", []string{"taskCreated"}, map[string]any{}, map[string]any{"task_id": "task_pub", "status": "SUBMITTED", "data": data})
			parsed := call("parseSubmitResponse", nil, map[string]any{"upstreamModel": model, "upstream": map[string]any{"kind": "new_api"}}, map[string]any{"body": created})
			assert.Equal(t, "task_pub", parsed["taskId"], model)
		}
	})

	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeNewAPI, ApiKey: "sk-gateway", ChannelBaseUrl: "https://gateway.example",
	}})
	success := map[string]any{"status_code": 0, "status_msg": "success"}
	for _, tc := range []struct {
		name        string
		presenter   string
		task        map[string]any
		wantStatus  string
		wantReason  string
		wantContent *channel.TaskContentRequest
	}{
		{
			name: "v1 before the first poll", presenter: "taskStatus", wantStatus: "IN_PROGRESS",
			task: map[string]any{"status": "SUBMITTED", "data": map[string]any{"task_id": "task_pub", "base_resp": success}},
		},
		{
			name: "v1 succeeded", presenter: "taskStatus", wantStatus: "SUCCESS",
			task: map[string]any{"status": "SUCCESS", "data": map[string]any{
				"task_id": "task_pub", "status": "Success", "file_id": "f1", "video_width": 1920, "video_height": 1080, "base_resp": success,
			}},
			wantContent: &channel.TaskContentRequest{
				URL:     "https://gateway.example/v1/tasks/task_pub/artifacts/video/content",
				Method:  http.MethodHead,
				Headers: map[string]string{"Authorization": "Bearer sk-gateway"},
			},
		},
		{
			name: "v1 failed by the gateway poller", presenter: "taskStatus", wantStatus: "FAILURE", wantReason: "task failed",
			task: map[string]any{"status": "FAILURE", "fail_reason": "task timed out", "data": map[string]any{"task_id": "task_pub", "status": "Processing", "base_resp": success}},
		},
		{
			name: "H3 before the first poll", presenter: "h3TaskStatus", wantStatus: "QUEUED",
			task: map[string]any{"status": "SUBMITTED", "data": map[string]any{"task_id": "task_pub"}},
		},
		{
			name: "H3 succeeded", presenter: "h3TaskStatus", wantStatus: "SUCCESS",
			task: map[string]any{"status": "SUCCESS", "data": map[string]any{"task": map[string]any{
				"id": "task_pub", "status": "succeeded", "resolution": "2K", "content": map[string]any{"url": "https://cdn.example/h3.mp4"},
				"usage": map[string]any{"output_seconds": 8, "input_seconds": 7.5, "input_image_count": 0},
			}}},
			wantContent: &channel.TaskContentRequest{URL: "https://cdn.example/h3.mp4", Method: http.MethodHead, Credentialless: true},
		},
		{
			name: "Zenmux H3 succeeded without an echoed model", presenter: "h3TaskStatus", wantStatus: "SUCCESS",
			task: map[string]any{"status": "SUCCESS", "data": map[string]any{
				"id": "task_pub", "status": "succeeded", "resolution": "2K", "content": map[string]any{"video_url": "https://video-product.cdn.minimax.io/h3.mp4"},
				"usage": map[string]any{"output_seconds": 5, "input_seconds": 0, "input_image_count": 0},
			}},
			wantContent: &channel.TaskContentRequest{URL: "https://video-product.cdn.minimax.io/h3.mp4", Method: http.MethodHead, Credentialless: true},
		},
		{
			name: "H3 failed by the gateway poller", presenter: "h3TaskStatus", wantStatus: "FAILURE", wantReason: "task timed out",
			task: map[string]any{"status": "FAILURE", "fail_reason": "task timed out", "data": map[string]any{"task": map[string]any{"id": "task_pub", "status": "running"}}},
		},
		{
			name: "H3 failed by MiniMax", presenter: "h3TaskStatus", wantStatus: "FAILURE", wantReason: "sensitive content",
			task: map[string]any{"status": "FAILURE", "fail_reason": "sensitive content", "data": map[string]any{"task": map[string]any{
				"id": "task_pub", "status": "failed", "error": map[string]any{"code": "1026", "message": "sensitive content"},
			}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.task["task_id"] = "task_pub"
			rendered := call("native", []string{tc.presenter}, map[string]any{}, tc.task)
			queryContext := map[string]any{}
			if tc.presenter == "h3TaskStatus" {
				queryContext["model"] = "MiniMax-H3"
			}
			result := call("parseTaskResult", nil, queryContext, rendered, map[string]any{"status": http.StatusOK})
			assert.Equal(t, tc.wantStatus, result["status"])
			assert.Equal(t, tc.wantReason, common.Interface2String(result["reason"]))
			if tc.wantContent == nil {
				return
			}
			facts := call("extractUsageOnComplete", nil, queryContext, nil, rendered)
			assert.NotEmpty(t, facts)
			assert.Equal(t, call("extractUsageOnComplete", nil, queryContext, nil, tc.task["data"]), facts)

			data, err := common.Marshal(rendered)
			require.NoError(t, err)
			task := &model.Task{TaskID: "task_down", Status: model.TaskStatusSuccess, Data: data, PrivateData: model.TaskPrivateData{UpstreamTaskID: "task_pub"}}
			artifacts, err := adaptor.ListArtifacts(task)
			require.NoError(t, err)
			assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
			descriptor, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
			require.NoError(t, err)
			assert.Equal(t, tc.wantContent, descriptor)
		})
	}

	t.Run("native bodies are checked before billing", func(t *testing.T) {
		for _, tc := range []struct {
			decoder string
			body    map[string]any
			wantErr string
		}{
			{"createTask", map[string]any{"model": "MiniMax-Hailuo-2.3", "prompt": "p", "duration": 10, "resolution": "1080P"}, "duration 10 only allows resolution 768P"},
			{"createTask", map[string]any{"model": "MiniMax-Hailuo-2.3-Fast", "prompt": "p"}, "supports image-to-video only"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "prompt": "p"}, "content must be an array"},
			{"createH3Task", map[string]any{"model": "MiniMax-H3", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "u"}}}}, "requires a non-empty text item"},
		} {
			_, err := plugin.Engine.CallPath(t.Context(), "native", []string{tc.decoder}, map[string]any{"body": map[string]any{"kind": "json", "value": tc.body}})
			require.ErrorContains(t, err, tc.wantErr)
		}
	})
}
