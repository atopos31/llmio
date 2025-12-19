package consts

type Style = string

const (
	StyleOpenAI    Style = "openai"
	StyleOpenAIRes Style = "openai-res"
	StyleAnthropic Style = "anthropic"
	StyleGemini    Style = "gemini"
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
