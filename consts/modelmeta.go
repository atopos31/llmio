package consts

// 模型元数据的来源名。
//
// 放在 consts 而不是 service/modelmeta 里，是因为它有两个消费方而它们
// 不能互相依赖：`models` 建默认策略时要写它，`service/modelmeta` 用它做
// 来源注册。它同时是 API 响应里 source 字段的取值——所以它是一份对外
// 契约，不是某个包的内部常量。
const (
	ModelMetaSourceModelsDev = "models.dev"
	ModelMetaSourceLiteLLM   = "litellm"
)

// ModelMetaDefaultSources 是策略里 sources 留空时按序尝试的来源。
var ModelMetaDefaultSources = []string{ModelMetaSourceModelsDev, ModelMetaSourceLiteLLM}
