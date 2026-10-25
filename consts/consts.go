package consts

type Style = string

const (
	StyleOpenAI    Style = "openai"
	StyleOpenAIRes Style = "openai-res"
	StyleAnthropic Style = "anthropic"
	StyleGemini    Style = "gemini"
	// StylePassthrough 提供商专属地址上未知路径的透传请求（见 handler/provider_route.go）。
	// 此类请求多数没有 token 用量可记，Size/ProxyTime 仍可观测。
	StylePassthrough Style = "passthrough"
)

const (
	// 按权重概率抽取，类似抽签。
	BalancerLottery = "lottery"
	// 按顺序循环轮转，每次降低权重后移到队尾
	BalancerRotor = "rotor"
	// 默认策略
	BalancerDefault = BalancerLottery
)

const (
	KeyPrefix = "sk-llmio-"
	KeyLength = 32
)
