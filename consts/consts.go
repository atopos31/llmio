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

const (
	// 流式请求超时时间缩短比例
	// 流式请求需要更快的首字节响应，因此超时时间设置为普通请求的 1/3
	StreamTimeoutDivisor = 3
	// 优雅关闭超时时间（秒）
	GracefulShutdownTimeout = 30
)
