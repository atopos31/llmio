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
	// ForwardRewriteNone 转发时原样保留客户端传来的模型名。
	ForwardRewriteNone = "none"
	// ForwardRewriteKnown 仅当模型在该提供商存在启用中的绑定时，改写为绑定上的上游模型名。
	ForwardRewriteKnown = "known"
	// ForwardRewriteAll 无条件改写为 ForwardTargetModel（Gemini 风格路径里的模型名一并改写）。
	ForwardRewriteAll = "all"
	// ForwardRewriteDefault 未显式设置时的策略：保留升级前的"认识才改写"行为。
	ForwardRewriteDefault = ForwardRewriteKnown
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
