package modelmeta

import (
	"net/http"
	"time"

	"github.com/atopos31/llmio/providers"
)

// fetchTimeout 是一次抓取的响应头超时。
//
// 5.3 MB（gzip 后约 531 KB）在慢线路上也可能要十几秒，30 秒是照着
// 「最慢的正常情况 + 余量」定的，不是照着快照定的。
const fetchTimeout = 30 * time.Second

// httpDoer 让两个 Source 的抓取可以被测试换掉。
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultHTTPClient 复用 providers 的客户端缓存，从而继承同一份代理配置。
//
// proxy 传空串走 http.ProxyFromEnvironment —— 这两个端点没有"属于哪个上游"
// 可言，环境变量里的代理是唯一合理的来源。
//
// 关于 gzip：**不显式设 Accept-Encoding**。Go 的 Transport 会在请求没有
// 该头时自己加上 gzip 并透明解压，效果与手写一遍相同（5,315,044 B → 530,868 B），
// 但少一处"设了头就得自己解压"的坑。
func defaultHTTPClient() httpDoer {
	return providers.GetClient(fetchTimeout, "")
}
