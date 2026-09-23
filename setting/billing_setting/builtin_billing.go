package billing_setting

// Built-in token prices use actual USD per million tokens. Keep new model
// defaults here instead of splitting them across the legacy ratio tables.
var builtinBillingExpr = map[string]string{
	// https://developers.openai.com/api/docs/pricing (Standard, 2026-09-09).
	// The Images API reports image output in output_tokens, normalized to c.
	"gpt-image-2":            `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-sunburst": `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-flare":    `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	// https://developers.openai.com/api/docs/models/gpt-6-astra
	// Standard pricing; the long-context rates apply to the whole request.
	// Do not infer service-tier discounts from incoming request parameters:
	// channels filter service_tier by default, so it may not reach the upstream.
	"gpt-6-astra": `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25)`,
	// OpenAI GPT-6 / GPT-5.6 published prices captured 2026-09-23. Requests
	// above 272K input tokens are billed at the long-context rates for the
	// whole request, so the tier condition uses len (the raw input length),
	// not the cache-discounted prompt count.
	"gpt-6-sol":     `len <= 272000 ? tier("standard", p * 2 + c * 10 + cr * 0.2 + cc * 2.5) : tier("long_context", p * 4 + c * 15 + cr * 0.4 + cc * 5)`,
	"gpt-6-luna":    `len <= 272000 ? tier("standard", p * 0.1 + c * 0.5 + cr * 0.01 + cc * 0.125) : tier("long_context", p * 0.2 + c * 0.75 + cr * 0.02 + cc * 0.25)`,
	"gpt-5.6-luna":  `len <= 272000 ? tier("standard", p * 0.2 + c * 1.2 + cr * 0.02 + cc * 0.25) : tier("long_context", p * 0.4 + c * 1.8 + cr * 0.04 + cc * 0.5)`,
	"gpt-5.6-sol":   `len <= 272000 ? tier("standard", p * 4 + c * 20 + cr * 0.4 + cc * 5) : tier("long_context", p * 8 + c * 30 + cr * 0.8 + cc * 10)`,
	"gpt-5.6-terra": `len <= 272000 ? tier("standard", p * 2 + c * 12 + cr * 0.2 + cc * 2.5) : tier("long_context", p * 4 + c * 18 + cr * 0.4 + cc * 5)`,
	// Anthropic Claude Opus 4.8 published prices captured 2026-09-23.
	"claude-opus-4-8": `tier("standard", p * 5 + c * 25 + cr * 0.5 + cc * 6.25)`,
	// DeepSeek published prices captured 2026-09-23. DeepSeek has no separate
	// cache-write price: a cache miss (write) is billed at the input rate.
	"deepseek-v4-flash":            `tier("standard", p * 0.15 + c * 0.6 + cr * 0.003 + cc * 0.15)`,
	"deepseek-v4-flash-vision-exp": `tier("standard", p * 0.15 + c * 0.6 + cr * 0.003 + cc * 0.15)`,
	"deepseek-v4-pro":              `tier("standard", p * 0.435 + c * 0.87 + cr * 0.003625 + cc * 0.435)`,
	// Alibaba Model Studio Qwen published prices captured 2026-09-23.
	// qwen3.7-plus switches to long-context rates above 256K input tokens.
	"qwen3.7-max":   `tier("standard", p * 2.5 + c * 7.5 + cr * 0.5 + cc * 3.125)`,
	"qwen3.7-plus":  `len <= 256000 ? tier("standard", p * 0.5 + c * 3 + cr * 0.05 + cc * 0.625) : tier("long_context", p * 2 + c * 6 + cr * 0.2 + cc * 2.5)`,
	"qwen3.8-flash": `tier("standard", p * 0.15 + c * 0.47 + cr * 0.016 + cc * 0.2)`,
	// Zhipu GLM published prices captured 2026-09-23 (cache writes are free).
	"glm-5.3-flash": `tier("standard", p * 0.15 + c * 0.5 + cr * 0.03)`,
}
