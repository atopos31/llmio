# llmio_dashboard → llmio 合并与整站重设计方案

> 状态：已评审 · 进行中（分支 `feat/console-redesign`）
> 拟稿 2026-09-29 · 最后更新 2026-09-29
> 目标：把 `E:\Projects\llmio_dashboard` 的能力并入 `E:\Projects\llmio`，**同时把整个管理台重新设计**——重设即合并方案。
> 前置阅读：`.impeccable.md`（设计上下文与已验证色板）

## 进度

| 步 | 状态 | 提交 |
|---|---|---|
| S1 后端聚合引擎 | ✅ 完成 | `3edf182` · `4a8c088` |
| S1.5 分时段计费（peak） | ✅ 完成 | `ba7fd88` |
| S2 设计系统落地 | ✅ 完成 | `6680760` · `ca6ddc7` |
| S3 新壳与导航 + 总览页 | 🟡 **大部分完成**：导航分组、三态主题、a11y、**总览页重做**已落地；**分析页未做** | `c162a84` · `43ea4a4` |
| S4 日志 / 对比 | 🟡 **大部分完成**：日志页 + 对比页 + 请求内容页已重做；**缓存前缀分析待补 tools 维度以外的细节** | `f979a5c` |
| S5 配额后端 + 配额页 | ⬜ 未开始 | — |
| S6 配置类页面 + 收尾 | ⬜ 未开始 | — |

S1 交付内容与偏差：

- 新增 `GET /api/metrics/stats`（单端点：趋势 / 五维下钻 / 延迟 / 错误 / 排行榜）与 `GET /api/metrics/granularities`
- `service/stats.go` 全部 30 个函数语句覆盖 **100%**；`handler/stats.go` 同理
- ChatLog 补 4 个索引（含 3 个复合），并展开 `gorm.Model` 以便给 `created_at` 打复合标签
- 顺带修掉一处既有问题：`models` 的测试未关闭 SQLite 连接，导致 `go test ./...` 在 Windows 上恒为红（`764f2f7`）
- **偏差**：既有的 `/api/metrics/use|counts|projects` 未按原计划改为转调新引擎。它们带 `topN=5` + `"others"` 塌缩，直接转调会改变响应结构并打断现有前端；留待 S3/S4 页面迁移后一并退役

S3 已完成的部分与实测结果：

- 导航按「观察 / 配置」分组；修正活跃态匹配（原先 `/logs/5/chat-io` 不高亮任何项）
- 三态主题切换接入顶栏，桌面端默认展开侧边栏
- **修正一处真实缺陷**：`App.tsx` 曾覆盖 `storageKey`，与 `index.html` 首屏脚本读取的键不一致
- 浏览器实测（Go 二进制 + Chrome）：三态切换、深色 token、自托管字体、嵌套路由高亮、刷新保持主题、无闪烁，全部符合预期
- 新增设计契约测试：色板 hex、深色两块一致、禁项、字体自托管、焦点环、reduced-motion、文档与代码一致。前端测试 101 个，纯逻辑层覆盖率 100%

**S3 已完成**（`43ea4a4`）：总览页重做，含 4.4 节图表修正清单的主体——

- 双轴拆成两张单轴图（请求数与 Token 各一张）
- 环形图改列表（错误类别：要比较的量，环形对接近值不可靠）
- 按实体稳定取色（新增 `useEntityColors`，以稳定全集分配槽位）
- 门槛染色改顺序色阶（首包耗时直方图）
- 状态强制「图标 + 文字 + 颜色」三者同现（新增 `StatusMark`）
- 浏览器实测：零横向溢出、图表填充确认为验证过色板且深浅自动切换、实测对比度全部 >=4.5:1

顺带修掉两处后端语义缺陷（均由真实数据暴露）：`retryRate` 出现 106.7% 这种非法的
「率」（实为平均每请求重试次数，已改名 `avgRetries`）；`byKey` 未按名称合并同名分组。
另为 Token 构成图补了 `TrendPoint.cached`。

**S3 剩余**：分析页 `/analytics`（趋势/下钻/延迟/错误四个维度视图与五维下钻表）。
其数据全部由已就绪的 `/api/metrics/stats` 一次返回。

**S4 已完成**（`f979a5c`）：日志页与对比页重做。

- 日志页：去掉双实现（原为表格 + 卡片列表两套）、冻结 ID 列、状态改用图标+文字+颜色、
  详情由弹窗改右侧抽屉、跨页多选对比、自动刷新（隐藏时跳过、回前台补一次）
- 对比页：**按缓存真实构造顺序 tools → system → messages 计算公共前缀**。
  原实现只比较 messages，而工具定义通常在最前面、体量最大——
  两个请求共享同一份 tools 时会误报前缀为零，把缓存命中的原因判断错
- 比较是字节忠实的：消除缩进噪音但不排序键（缓存看到的也是原始键序）

真实数据实测：公共前缀 5 项（工具 2 · 系统 1 · 消息 2），四条请求的起分叉处各不相同，
其中一条被正确识别为「完整包含公共前缀」。

---

## 0. 决策台账

| # | 决策 | 取值 | 来源 |
|---|---|---|---|
| D1 | 前端形态 | 移植进 React 管理台，**一套 UI 体系** | 用户 |
| D2 | 配额子系统 | **全部 Go 重写**；复杂登录型源升为 Go 内置适配器，goja 只服务长尾脚本 | 用户 + §3.4 |
| D3 | 交付形态 | **单二进制**，无外部运行时 | 用户 |
| D4 | 重设范围 | **完全重新设计**——整个管理台，不受现有布局与视觉约束 | 用户 |
| D5 | 重设与合并关系 | **重设即合并方案** | 用户 |
| D6 | 使用者 | 仅本人；无角色分级 | 用户 |
| D7 | 主题 | 深浅两套同等做工，跟随系统 + 可切回 system | 用户 |
| D8 | 气质 | "炫酷科幻灵动" → 解读为**仪器感 / 有生命 / 不喧哗** | 用户 + 本文档 |
| D9 | 无障碍 | AA 对比度 · 色盲友好 · 完整键盘可用 · **不可有文字遮挡** | 用户 |
| D10 | 分支 | 全部改动在 **`feat/console-redesign`** 上 | 用户 |
| D11 | 测试覆盖 | **目标 100%**，分层执行（见 §5） | 用户 |

> **D4 已确认为"完全重新设计"**：第 4 节是完整的重设方案，不预设沿用现有布局、信息架构或视觉。现有代码只作功能与数据的参考。

---

## 1. 现状盘点

### 1.1 两个项目

| | llmio | llmio_dashboard |
|---|---|---|
| 形态 | Go 单体 + React 管理台（embed） | Node 服务 + Vue3 SPA |
| 职责 | LLM 网关：路由、负载均衡、日志落库 | 分析面板 + 配额/余量 |
| 数据 | 自有 SQLite（`chat_logs` / `chat_ios` / …） | 通过 HTTP 读 llmio + 独立配额配置 |
| 技术栈 | Gin + GORM + React19/Tailwind4/Recharts | 裸 `http` + Vue3/Element Plus/ECharts |
| 视觉 | shadcn neutral 中性 | Element Plus 默认蓝 |
| i18n | 三语（zh-CN / zh-TW / en） | 无，硬编码简体中文 |
| 主题 | 深浅（实现有缺陷，见 1.3） | **无深浅，仅浅色** |

### 1.2 关键发现：合并后可以删掉一大层

这是整个方案形态的转折点。dashboard 存在的理由写在 `server/llmio.js` 头部注释里：**当时的 llmio 上游会接受 `status/provider/model/key_name` 过滤参数但静默忽略，`sort`/`order` 也无效，`page_size` 上限 100**，所以它必须把全量日志拉到本地、在内存里过滤排序分页。

**当前 master 的 llmio 已经原生支持这些过滤**（`handler/api.go:770-861`：`provider_name` / `name` / `status` / `style` / `auth_key_id` / `trace_id` / `session_id` / `id`，`ORDER BY id DESC`，分页上限 100）。且 `/api/logs/:id/chat-io` 已存在。

因此**整层 Node 代理可以删除**：

| dashboard 组件 | 处置 |
|---|---|
| `server/index.js` 全部路由 | ❌ 删除（llmio 已有等价或更强的原生端点） |
| `server/llmio.js`（代理 + 分页拉取 + 缓存 + `.env`） | ❌ 删除 |
| `/api/logs` 内存过滤/排序/分页 | ❌ 删除（改用 llmio 原生查询参数） |
| `/api/logs/:id/chat-io` 透传 | ❌ 删除（`GET /api/logs/:id/chat-io` 已存在） |
| `/api/providers`、`/api/models` 透传 | ❌ 删除（原生存在，且原本就没被调用） |
| `server/stats.js` 聚合引擎 | ✅ **移植成 Go**（第 3.2 节）——这是唯一必须移植的后端逻辑 |
| `server/quota/**` 配额子系统 | ✅ **移植成 Go**（第 3.3 节）——真正的新增 |
| 跨域 + 无鉴权 + `Allow-Origin: *` + 5s/60s 缓存 | ❌ 全部消失（同源、走 `Authorization: Bearer`、无中间层） |

顺带消失的还有：上游 IP `129.211.169.240:7070` 的三处硬编码、浏览器永不接触上游 token 的那套隔离（同源后天然成立）、`createWebHistory` 缺少 SPA 回退的问题（llmio 的 `setwebui` 已实现回退）。

### 1.3 llmio 现有前端的真实缺陷（本次一并修）

| 缺陷 | 位置 | 说明 |
|---|---|---|
| **深色变体与主题实现不一致** | `webui/src/index.css:4` vs `components/theme-provider.tsx` | `@custom-variant dark` 声明匹配 `[data-theme=dark]`，但 provider 实际加的是 `.dark` 类。当前深色样式靠变量覆盖块 + Tailwind 默认 `prefers-color-scheme` 变体在兜，声明的 variant 实际失配 |
| **主题切不回 system** | `routes/layout.tsx` | 切换按钮是 `light ↔ dark` 二值，用户选了 system 后无法回去 |
| **图表色是 shadcn 遗留** | `index.css:44-121` | `--chart-1..10` 是随手排的，未经验证；且 `chart-6..10` 未在 `@theme` 声明，只能靠内联 `var()` 取用 |
| **配色跟随"响应顺序"而非实体** | `components/charts/*.tsx` | 四张图各自用本地 `predefinedColors` 数组、按 `ChartConfig` 的 key 顺序取色。数据一变（如 topN 塌缩），存活系列会被改色 → 违反"颜色跟随实体，永不跟随排名" |
| **绝对禁止的侧边条纹** | 迁移源（dashboard `Logs.vue`） | 错误行用"红底 + 3px 左边框"。这是最典型的 AI 设计指纹，**不得移植** |
| **文档过期** | `CLAUDE.md:95` | 写着"SWR for data fetching"，实际 `swr` 既不在依赖里也不在代码里；数据获取全是手写 `useState + useEffect` |
| **文档过期** | `CLAUDE.md:68` | 把 `handler/home.go` 描述为"静态文件服务"，它实际是**指标聚合**（静态服务在 `main.go` 的 `setwebui`）；`handler/models.go` 也被误述 |
| **巨型文件** | `routes/model-providers.tsx` 1521 行 / `log-chat.tsx` 1001 行 / `auth-keys.tsx` 898 行 / `logs.tsx` 710 行 | 重设时顺势拆分 |

### 1.4 必须保留的能力清单（迁移核对表）

来自 dashboard，全部需要在合并后仍然可用：

**分析类**
1. 三态请求模型（`success` / `running` / `error`），在途请求单独呈现、不计入成功率分母
2. KPI 组：总量、成功率、Token 三档、缓存命中率、重试数与重试率、平均 TPS、首包 P50
3. 时间序列趋势（自动分桶，档位 `5m/15m/30m/1h/2h/6h/12h/1d/7d`，目标约 60 桶）
4. 五维下钻：模型 / 供应商 / Key / 请求名 / UserAgent，每维含成功率、平均+最大 TPS、平均+P95 首包、缓存命中率、重试数、平均代理耗时
5. 首包延迟分布（直方图 + P50/P90/P95/P99 最近秩分位）
6. 错误分类学（余额不足402 / 限流429 / 鉴权401 / 超时 / 上游5xx / 网络 / 其他），每类含受影响供应商+模型+可点击样本
7. TPS 榜首 Top10 / 最慢首包 Top10
8. 成本合计（`input_price` + `output_price`，含币种）
9. 请求内容重建：SSE 流式分片重组、`reasoning_content`（思考）块、流式 `tool_calls` 参数分片合并、tool 结果反查来源工具调用
10. 多请求对比：公共前缀分析（消息级分叉位置 + 字符级公共前缀长度）与缓存命中率关联——**这是"为什么缓存没命中"的根因工具**
11. 可深链的请求详情页与对比页

**体验类**
12. 可配置的看板卡片显隐/排序（本地持久化）
13. 可配置的日志表列显隐/排序（本地持久化）
14. 自动刷新：日志/看板与配额两套独立档位；标签页隐藏时暂停、回到前台立即补一次；失败静默且保留旧数据；"更新于 x 前"实时指示
15. **刷新时保留对比选中项**（按 ID 重映射）
16. 后台刷新**不出现骨架闪烁**（保持旧渲染降透明度）

**配额类**
17. 三类数据源：内置适配器 / 可配置 HTTP / 任意脚本
18. 宽松归一契约（"三者任意两个"、中英文单位与窗口别名、时间强制、状态派生、格式模板 DSL）
19. 脚本在线编辑（多语言）+ **试跑（不保存）** 并回显解析结果与原始 stdout/stderr + 模板载入
20. 每源独立刷新（不连带重跑其他慢源）
21. 从 llmio 供应商一键发现与导入（密钥不落浏览器）
22. 凭据全链路掩码
23. 逐源/逐条的展示自定义（改名、改格式、隐藏、图表样式）
24. 写操作保护

---

## 2. 目标形态

### 2.1 信息架构

按"观察 / 配置"两分为主导航。现有导航是 7 项平铺，加入分析类后必然拥挤，且分析类与配置类的使用动机完全不同（一个看，一个改）。

```
观察 ── 总览        /                    首屏：现在正在发生什么
        分析        /analytics           趋势 · 下钻 · 延迟 · 错误
        日志        /logs                请求明细（筛选行 + 表 + 详情抽屉）
        对比        /compare?ids=…       多请求对比 · 缓存前缀根因
        配额        /quota               余量控制台
配置 ── 供应商      /providers
        模型路由    /model-providers     模型 ↔ 供应商关联编排
        密钥        /auth-keys
        系统        /config             计数令牌配置 · 日志清理策略
```

页内层级用**分段控件（segmented control）**而非并列卡片——现有实现是把 9 张卡片堆在一页，靠滚动找人。总览只留结论，细节下沉到分析页。

### 2.2 设计方向

见 `.impeccable.md`。要点：**科幻感来自精密与实时，不来自霓虹与发光**。参照航天任务控制台、航空仪表、示波器面板、印刷技术手册。

- 首屏**遥测优先**：回答"正在发生什么"，不是陈列累计数字
- 颜色**只作信号**，不作装饰
- 动效只在状态变化处出现（在途、到达、切换、重排），指数减速，禁回弹
- 明确反参考：霓虹赛博朋克、青蓝发光 HUD、扫描线贴图、SaaS 大数字KPI阵列、玻璃拟态、Orbitron 类科幻字体、影视 HUD 六边形

字体：一个有个性的拉丁显示字（数字用，自带 tabular figures）+ 高度可读正文 + 等宽用于数值/ID/表头。**中文回退系统 CJK 栈**（PingFang SC / Microsoft YaHei / Noto Sans CJK），不为设计感牺牲中文渲染。字形**自托管子集**，不引第三方 CDN（离线单二进制工具的硬约束）。

### 2.3 已验证色板

分类色板 8 槽，固定顺序，永不循环。验证器结果（脚本实算，非目测）：

| 槽 | 色相 | Light | Dark |
|---|---|---|---|
| 1 | 靛蓝 | `#3b5fdc` | `#6789f0` |
| 2 | 铜金 | `#c2631c` | `#d9743a` |
| 3 | 青绿 | `#0d9488` | `#16a99b` |
| 4 | 琥珀 | `#b5830c` | `#b87f00` |
| 5 | 品红 | `#cf4585` | `#d4529b` |
| 6 | 绿 | `#167a37` | `#2aa85e` |
| 7 | 紫罗兰 | `#6b48c2` | `#8d76e0` |
| 8 | 红 | `#cf4444` | `#e26a6a` |

- Light（surface `#fbfbfc`）：相邻 CVD ΔE **11.5** · 常视觉 ΔE **19.0** · 对比度全 ≥3:1 → 全项 PASS
- Dark（surface `#14161c`）：相邻 CVD ΔE **8.3** · 常视觉 ΔE **20.1** · 对比度全 ≥3:1 → 全项 PASS
- 全配对形态（散点等）**前 3 槽**两模式均通过 → 超过 3 系列必须折叠"其他"或分面
- 铜金顺序色阶：Light `#e2a066 → #6b3710`，Dark `#f0d2a8 → #8a4f1c`
- 状态色固定：good `#0ca30c` · warning `#fab219` · serious `#ec835a` · critical `#d03b3b`，**必配图标+文字**

---

## 3. 后端设计

### 3.1 模块布局

```
service/
  stats.go          ← 新：聚合引擎（移植自 stats.js）
  quota.go          ← 新：配额编排、缓存、发现/导入
quota/              ← 新包
  contract.go         归一契约 + 格式模板引擎
  builtin.go          内置适配器注册表
  http.go             HTTP 适配器（占位符/鉴权/点路径映射）
  script.go           goja 脚本执行
  scriptstore.go      脚本落盘
  config.go           配额配置读写
handler/
  stats.go          ← 新：分析端点
  quota.go          ← 新：配额端点
models/
  init.go           ← 改：新增 AutoMigrate 目标
```

### 3.2 分析聚合引擎

**端点设计：单端点 `GET /api/metrics/stats`**，而非拆成 6 个。

理由：设计规范要求"筛选行位于所有图表之上，筛选作用于其下的一切，所有图表对同一切片重新渲染，数字必须相互吻合"。一个请求 = 一个切片 = 天然一致。拆成多端点会引入请求间的时间窗漂移和 N 次往返。

```
GET /api/metrics/stats
  from, to          时间范围（RFC3339 或 unix）
  granularity       auto|5m|15m|30m|1h|2h|6h|12h|1d|7d
  provider, model, name, status, key_id, ua   逗号多值
→ {
    generatedAt, bucketMs,
    range: { from, to },
    kpi:     { total, success, failed, running, finished, successRate,
               promptTokens, completionTokens, totalTokens, cachedTokens, cacheHitRate,
               cost, currency, totalRetries, retryRate },
    trend:   [{ ts, total, success, error, running, tokens, prompt, completion,
                avgTps, avgFirstChunkSec }],
    byModel  byProvider  byKey  byName  byUa : [GroupStat],
    errors:  [{ code, type, count, providers[], models[], samples[] }],
    errorTrend: [{ ts, count }],
    latency: { firstChunk:{p50,p90,p95,p99,list[]}, tps:{avg,max,p50,p95,list[]},
               proxyMs:{avg,p95,list[]} },
    topTps: [...10], slowest: [...10], recentErrors: [...10]
  }
```

移植要点（逐条对应 `stats.js`）：

- **分桶阶梯** `resolveBucketMs`：命名档位映射 + `auto` 走"漂亮阶梯" `[5m,15m,30m,1h,2h,6h,12h,24h,7d]`，目标 ≈60 桶。注意原实现的阶梯比显式映射多出 `2h/12h/7d`，仅 `auto` 可达——**建议统一**，让显式档位与阶梯同集合
- **单位启发式** `nsToMs`：`>1e6` 视为纳秒否则毫秒。**这是脆弱启发式**，应在移植时改为按字段语义硬编码（`ChatLog.ProxyTime`/`FirstChunkTime`/`ChunkTime` 都是 Go `time.Duration` = 纳秒；`Tps` 是 float64），不要保留猜测分支
- **分位数**：原实现是最近秩 `floor((n-1)*p)`，非插值。保留该定义并在 UI 注明，避免与直觉差异被当成 bug
- **成功率分母**：`success / (success + failed)`，**排除 running**。这是刻意设计，保留并在 UI 用文案体现
- **错误分类学**：`KNOWN_ERRORS` 七类，首匹配胜出，`raw` 截断 500 字符。移植为有序规则表，可配置
- **成本**：`Σ input_price + Σ output_price`（价格是请求时的快照，已随行落库，聚合无需 join）

**索引（必做）**：`ChatLog` 目前单列索引有 `name/trace_id/provider_model/provider_name/status/user_agent/auth_key_id/session_id`，但 **`created_at` 没有索引**，而所有聚合都按 `created_at` 过滤与分桶。需新增：

```
created_at                单列
created_at, status        复合（趋势与成功率）
auth_key_id, created_at   复合（Key 维下钻）
name, created_at          复合（模型维下钻）
```

GORM `AutoMigrate` 启动时自动建，无需手写迁移。

**旧端点处置**：`/api/metrics/use/:days`、`/api/metrics/counts`、`/api/metrics/projects` 保留兼容（前端旧代码与任何外部调用），但实现改为转调新引擎。注意 `counts`/`projects` 有 `topN=5` + "others" 塌缩——这正违反"颜色跟随实体而非排名"，新前端不再使用它们，仅在旧端点里保留原语义。

### 3.3 配额子系统（Go）

**契约移植**（`contract.js` → `quota/contract.go`）——逐项保留：

| 能力 | 说明 |
|---|---|
| 宽松输入 | 接受 `{items:[]}` / 裸数组 / `{data:[]}` / 对象映射 / 单个裸对象 |
| 字段别名 | `used\|usedAmount\|usage\|已用` 等；`total`、`remaining` 同理 |
| 三者任意两个 | 缺一补一；`unit:'%'` 无 total 时 total 默认 100 |
| 无有效数值 | 报错"数据源返回了 JSON，但没有任何可识别的余量数值" |
| 单位归一 | `% / CNY / USD / tokens / CREDITS / 次`，派生出 `percent/money/amount/count` 四类格式化 |
| 窗口归一 | 含中文别名 `5小时/日/天/周/月/总额/余额` → `5h/day/week/month/total` |
| 时间强制 | 数值 <1e11 视为秒否则毫秒；空格分隔时间补 `T`；斜杠转破折号 → ISO |
| 状态派生 | 显式优先；否则按 `remaining<=0` / `percent>=100` / `percent>=warningAt` / 全空 → `exhausted/warning/unknown`；整源取最差 |
| 格式模板 DSL | `{used} {total} {remaining} {percent} {unit} {label} {window}` + `{used:2}` 精度语法 |
| 模糊 JSON 抽取 | 代码块围栏 → 整体解析 → 字符串感知的平衡括号扫描 + 候选打分（`{items:[]}` 100+ / `{data:[obj]}` 90 / …），最多 80 候选 |

> **格式模板引擎原为前后端各一份、靠注释要求手工同步**（`contract.js` ↔ `quotaFormat.js`）。合并后**只保留 Go 一份**，服务端算好 `item.text` 下发，前端只做自定义覆盖的实时预览（用同一份语法的 TS 镜像，但**不再是真相来源**）。这消除了一处必然漂移的重复。

**三类数据源**

- `builtin`（`quota/builtin.go`）：`deepseek`（已实测）/ `moonshot` / `scnet`（**保留为"官方无用量端点"的报错桩**）/ `custom`（通用端点，走 `itemsPath` + `map`）。支持 `path/method/query/headers` 覆盖；一旦用户给出 `itemsPath` 或非空 `map`，就绕过内置 `parse()` 走通用映射
- `http`（`quota/http.go`）：纯配置。占位符 `{{apiKey}} {{baseUrl}} {{id}} {{name}}` 插值（url / 每个 header / auth.user / auth.token / 字符串 body）；鉴权四型 `bearer|header|basic|none`；`pick(obj,"a.b.c")` 点路径；`=` 前缀表示字面量；`constants` 先作默认
- `script`（`quota/script.go`）：见 3.4

**配置与凭据**

- 配置落 **`./db/quota.config.json`**（与 SQLite 同目录、二进制外、易备份、可手工编辑），路径可用 `LLMIO_QUOTA_CONFIG` 覆盖。**不放进 `configs` KV 表**——配额配置与脚本是文件形态的运维资产，且原项目的可手工编辑性是有意设计
- 响应**永不回传明文**：`apiKey` → `apiKeyMasked` + `hasApiKey`；`env` 中键名匹配 `/pass|secret|token|key|cookie/i` 的一律掩码；`scriptSource` 例外（编辑器必须显示）
- 写入回填：入参为空或含 `****` → 保留原存储值
- 每源/每项展示覆盖、图表样式等**纯前端偏好**保留为浏览器本地存储（属"per-viewer convenience"，不上升为服务端状态）

**缓存与扇出**：`Map<id, {ts,data}>`，TTL = `max(10s, refreshInterval)`；`runAll` 并发跑全部启用源，非 force 时复用未过期条目；返回 `{generatedAt, refreshInterval, warningAt, sources[], summary{}}`，`summary.worst` 取所有 ok 源所有项里的最差。**每源独立刷新**用"先 force 单源、再取整体（其余走缓存）"两步实现——保留这个技巧。

**发现与导入**：按供应商名启发（`/deepseek/i`→内置，`/moonshot|kimi/i`→内置，`/scnet|超算/i`→脚本，否则 http）；解析上游 `Provider.Config` 取 `base_url`/`api_key`；**密钥在服务端直接写入配额配置，不经过浏览器**——同源后这条依然要显式保持（服务端读上游 config 即可，前端只提交 `upstreamId`）。

**写保护**：原设计是"仅回环 + 显式开启"，理由是"写权限 = 可在主机上执行任意命令"。合并后 **goja 沙箱替换了 OS 进程 spawn，且整个 `/api` 已在 TOKEN 之后**，这条限制的存在前提消失。建议**降级为一个开关**（保留 `LLMIO_QUOTA_ALLOW_WRITE=false` 的只读模式语义），不再做回环判断。

### 3.4 脚本沙箱（本方案风险最高处）

**这是唯一一处"用户在浏览器里编辑、服务端执行"的代码路径。** 原实现是 `child_process.spawn`，**无沙箱、无用户隔离、继承服务端全部环境变量**，注释中已自认这等价于主机 RCE。

选型 **goja**（纯 Go 的 ES5.1+ 引擎，无 cgo）：保持 `CGO_ENABLED=0` 静态单二进制，符合 D3。

**必须同时解决的三件事：**

**(1) 沙箱** — 默认 **零宿主 API**。脚本拿到的全局仅：

```
console.log/warn/error   → 收集到 stdout 缓冲（有上限、有截断标记）
fetch(url, opts)         → 受限 HTTP，仅 http/https，超时与体积上限，禁内网地址
env                      → 只读，仅暴露该源显式配置的 env 白名单
crypto                   → 由宿主提供（RSA 公钥加密、HMAC、base64、随机字节）
output(value)            → 提交契约 JSON
```

- **阻断**：无 `require`/`import`、无文件系统、无 `child_process`、无 `eval` 逃逸面
- **超时**：goja 的 `Interrupt` 按 `timeout`（默认 30s）中断，**这是硬中断**，不是协作式取消
- **内存/输出上限**：输出缓冲设上限并标记截断（保留原 `MAX_OUTPUT` 语义）
- **并发**：全局信号量限制同时在跑的脚本数，防单个慢源拖垮面板
- 失败语义保留：非零退出但解析出数据 → 降级为 `warning`；超时 → 明确报"执行超时"；解析优先 stdout，**仅在成功时**回退 stderr（避免把错误栈里的 JSON 片段误当数据）

**(2) 现有两个脚本必须重写** — `scnet-quota.js` 与 `opencode-quota.js` 重度依赖 Node 专有面：`crypto.publicEncrypt`（RSA-512）、手写 cookie jar、手动跟随重定向（深度 8、每跳重新吸收 cookie）、`fetch`。它们**无法原样跑在 goja 上**。

**(3) 因此建议：把这两个脚本提升为 Go 内置适配器**

| 方案 | 评价 |
|---|---|
| A. 提供 goja fetch/crypto 宿主 API，把两个 Node 脚本改写成 JS | 可行，但把"复杂登录态"逻辑留在沙箱内，调试难、能力受限、易被宿主 API 的边界卡住 |
| **B. 两个脚本改为 Go 内置适配器；goja 只服务用户自写的长尾** ⭐ | 复杂登录流用 Go 原生 `net/http` + `crypto/rsa` + `net/http/cookiejar` 实现，健壮且可测；goja 沙箱面积因此**保持极小**，安全边界清晰 |

**已定方案 B**：`scnet`（RSA 加密 SSO 登录 + 重定向 + cookie jar + 双端点 join 出窗口定义）与 `opencode`（会话 Cookie + `x-org-id`）**升为 `quota/builtin.go` 里的一等内置适配器**——复杂登录流用 Go 原生 `net/http` + `crypto/rsa` + `net/http/cookiejar` 实现，健壮、可测、可断言。goja 退化为"接一个冷门供应商又不想重编译"的逃生舱，这正是它应有的定位。

**因此宿主 API 初期只给三个**：`fetch` · `env` · `console`。`crypto` 与 `output` 之外的一切都等真有需求再加——沙箱面积越小越安全。

### 3.5 数据与迁移

- `models/init.go` 的 `AutoMigrate` 需新增本方案引入的所有模型（当前 8 个：Provider / Model / ModelWithProvider / ChatLog / ChatIO / Config / AuthKey / LogCleanupRecord）
- `ChatLog` 新增复合索引（3.2 节）
- 配额配置**不进 DB**，走 `./db/quota.config.json`
- 存档 `llmio_dashboard`：打 tag 或移到 `archive/`，防止两处逻辑各自演化

---

## 4. 前端设计

### 4.1 设计 token 与主题修复

**先修主题机制再谈皮肤**，否则新 token 会挂在错误的开关上。

1. **统一为 `data-theme` 属性**（弃用 `.dark` 类）。理由：与现有 `@custom-variant` 声明对齐；`data-theme` 可被 `light-dark()` 与未来 CSS 采用；也便于在 `:root` 上同时声明"跟随系统"与"手动覆盖"两层
2. 主题状态改为三值 `light | dark | system`，**切回 system 要能生效**（当前二值切换是缺陷）
3. 深色值需在**两个作用域**下声明：`@media (prefers-color-scheme: dark)` 由 `:root:not([data-theme=light])` 守卫，以及 `:root[data-theme=dark]`——使手动覆盖双向都胜出

**token 分层**（替换现有 `--chart-1..10` + 散落的硬编码）：

```
--surface-1 / --surface-2       页面面 / 卡片面（中性向品牌色相微调，chroma 0.005–0.015）
--ink-1 / --ink-2 / --ink-3     主 / 次 / 弱文字
--rule / --rule-strong          发丝分隔线 / 轴线
--accent                        铜金（唯一强调色，10% 原则）
--series-1..8                   已验证分类色板（2.3 节）
--seq-1..5                      铜金顺序色阶
--status-good/warning/serious/critical   固定状态色
```

**保留**：Tailwind v4 + `@theme` 映射方式、`--radius` 派生、Radix 原语、`class-variance-authority`、三语 i18n 结构（每个路由一个 namespace 的约定）。
**移除**：`--chart-1..10`（换为 `--series-1..8`）、`App.css`（死文件，无人 import）、`index.html` 里的 Google Fonts CDN 引用（改自托管）。

### 4.2 壳与导航

- 主导航分 **观察 / 配置** 两组（2.1 节），侧边栏沿用可折叠（`min-w-48` / `min-w-14`）
- **修 `navItems` 的活跃态匹配**：当前是 `location.pathname === item.to` 精确匹配，导致 `/logs/5/chat-io` 不高亮任何项。改为前缀匹配 + 最长前缀胜出
- 顶栏：品牌 + 版本 Badge + 语言选择 + **三态主题切换** + 登出
- 版本更新检查（`checkLatestRelease` 打 GitHub 公共 API）保留，但**不要在离线环境阻塞或报错**，且提示改为非模态——独立深色/离线工具里弹窗打断不合适

**需要补齐的 Radix 原语**（现有 19 个里没有）：`tabs`（页内分段）、`dropdown-menu`（行操作，当前靠裸按钮堆叠）、`separator`、`scroll-area`（长列表虚拟滚动）、`skeleton`、`toggle-group`（时间范围/档位切换）、`collapsible`（现为手写，`log-chat.tsx:62`）、`progress`/`meter`（配额）。全部走 shadcn `new-york` 风格，与既有 `components.json` 一致。

### 4.3 页面设计

**总览 `/`** — 首屏回答"现在正在发生什么"
- 一个 **hero figure**：全页仅此一个 ≥48px 的数字（建议"当前在途请求数"或"今日请求数＋同比"），同在无衬线体、**比例数字**（非 tabular）
- 一条 **KPI 行**（不是 4 张大卡）：今日/近30日 请求 · Token · 成功率 · 缓存命中率 · 重试率
- **趋势图**：请求与 Token **分成两张**（见 4.4，原双轴必须拆）
- **配额条**：紧凑只读，最紧张的一条置前，点击进配额页
- **最近失败**列表（可点击进详情）
- 累计量（30日总量）**降级为次要信息**，不做首屏主角

**分析 `/analytics`** — 细节下沉的目的地
- **筛选行在最上，一行**，作用于其下全部：时间范围（预设行 + 自定义）、供应商、模型、Key、请求名、状态
- 分段控件切换维度视图：趋势 / 下钻 / 延迟 / 错误
- 下钻表：模型·供应商·Key·请求名·UA 五维，列含成功率、平均+最大 TPS、平均+P95 首包、缓存命中率、重试、平均代理耗时
- 延迟：直方图 + P50/P90/P95/P99，**分位数定义（最近秩）在 UI 注明**
- 错误：**分类用条形而非环形**（见 4.4），每类展开显示受影响供应商/模型 + 可点样本
- TPS 榜首 / 最慢首包 两张榜

**日志 `/logs`**
- 筛选行 + 12 列表格（去掉现有 `sm:hidden` 卡片列表那套双实现——改为单表格 + 横向滚动 + 冻结首列，避免两份逻辑漂移）
- 行内**不再用红底+左条纹**（原文的设计指纹）。错误/在途改用：状态图标 + 文字 + 极轻的行底色（≤表面 5% 偏移）
- 选中行的"对比"工作流保留，上限 6，非 success 不可选
- 详情抽屉：身份 / 性能 / Token 与费用 / 错误，保留单位自适应格式化

**请求内容 `/logs/:id/chat-io`**
- 保留流式分片重组、`reasoning_content`、`tool_calls` 参数分片合并、tool 结果反查
- **markdown 渲染需换**：原实现是正则拼 HTML（无列表/链接/表格/行内代码）并 `v-html` 注入——既有 XSS 面也有功能缺口。改用成熟渲染器 + 默认转义
- 消息体折叠阈值保留（800 字符），但**渐隐遮罩不要用硬编码 `#fff`**（深色下会错）

**对比 `/compare?ids=`**
- **分叉树不用 ECharts tree**：改为 DOM 渲染的缩进共享前缀树——键盘可遍历、可选中、可复制，且不引 ECharts
- **缓存前缀分析是这一页的真正价值**：消息级分叉位置（"第 N 条起分叉" / "完全一致"）+ 字符级公共前缀长度 + 与观察到的缓存命中率并列
- 指标对比表保留

**配额 `/quota`**
- 控制台形态：摘要条（最紧张 / 源计数 / 失败数）+ 卡片网格
- **图表样式四选**保留（`progress` / `ring` / `bar` / `text`），但 `ring` 由 ECharts gauge 改为 **meter**（规范：单一比例对上限 → meter，不是仪表盘/pie）
- 数据源编辑器：按类型分派的表单，**内置/HTTP/脚本三态**，高级项折叠
- **试跑（不保存）面板保留**——它是整个配额功能里最有价值的作者循环：显示 ok/失败、耗时、解析出的条目及其渲染文本与状态、以及折叠的原始 stdout/stderr/退出码
- 单条自定义：格式模板 + **可点击 token 芯片** + 实时预览
- 从上游导入：发现表 + 掩码密钥 + 建议标签 + 逐行导入
- 只读模式提示保留

**配置类页面**（供应商 / 模型路由 / 密钥 / 系统）
- 视觉随新体系重做，**信息架构基本保留**
- `model-providers.tsx`（1521 行）按职责拆分：模型列表 / 关联列表 / 表单对话框 / 连通性测试
- 模型拖拽排序：**必须补键盘路径**（当前只有指针拖拽，违反 D9"完整键盘可用"）
- 系统页沿用"每项设置一张卡 + 独立 schema + 对话框"的现有形态（`config.tsx` 是很好的模板）

### 4.4 图表规范修正清单

迁移过来的图表有几处系统性错误，**不是风格偏好，是规范硬约束**。逐条修正：

| 原实现 | 问题 | 修正 |
|---|---|---|
| `trend` 卡片：请求与 Token 双轴（`yAxisIndex:1`） | **双轴图禁项**——两轴缩放对齐是任意的，会凭空造出数据里没有的相关性 | 拆成两张图；或都指数化到共同基准（t=100）走单轴 |
| `perf` 卡片：TPS + 首包 + 缓存率**三轴**组合 | 同上，且更严重 | 拆为小倍数（small multiples），或各自成图 |
| `errors` 用环形图 | 环形/饼图用于比较接近的值不可靠 | 改条形；类别 >7 时直接上表 |
| `success` 用环形图 | 三态占比用环形尚可，但堆叠条更准且省空间 | 改水平堆叠条 + 分隔间隙 |
| `latency` 直方图：≥5s 的柱子染红 | 数值渐变冒充状态色，且红/绿对立不满足色盲友好 | 单色阶（铜金顺序色阶）；若"慢"确为状态语义，则用固定状态色 **并配图标/文字** |
| 配额 `ring` 用 ECharts gauge | 单一比例对上限错用仪表盘 | 改 meter（同色阶轨道 + 填充承载严重度） |
| 图表取色按 `ChartConfig` key 顺序 | **颜色跟随排名而非实体**——筛选后存活系列被改色，读者学到的"某模型是蓝色"失效 | 按稳定实体键（模型名/供应商名）分配槽位并固定映射 |
| 表格 TPS / 缓存率单元格按阈值染色（≥50绿/≥20蓝…） | 阈值色等于把连续量伪装成状态；且绿/蓝/橙/红四色对立不利于色盲 | 用单一顺序色阶 + 数值本身；确需阈值告警时用状态色 + 图标 |
| 配额图卡 hover 才显示编辑铅笔 | 悬停才暴露的次要操作尚可，但**不能是唯一入口** | 保留 hover 显隐，但键盘 focus 必须等效触发 |
| 所有图 | 缺 hover/tooltip 层、缺表格视图 | **图表默认交付交互层与表格视图**——tooltip 只增强不把关，键盘 focus 与 hover 同效 |
| 大数用 `tabular-nums` | 大号独立数字用等宽数字会显松散 | hero 与 stat tile 用**比例数字**；`tabular-nums` 仅用于需纵向对齐的表格列与轴刻度 |

**系列上限**：分类色板 8 槽，**超 8 必须折叠"其他"或分面**，绝不生成第 9 色。散点等全配对形态**上限 3 系列**（已验证）。

---

## 5. 测试策略（D11：目标 100%，分层执行）

### 5.1 现状

| | 现状 | 缺口 |
|---|---|---|
| Go | 7 个测试文件（`balancers/` 2 · `service/` 3 · `middleware/` 1 · `models/` 1） | **`handler/` 零测试**；`common/`、新增的 `quota/` 与 `service/stats.go` 全无覆盖 |
| 前端 | **零测试基础设施** | 无 vitest / jest / testing-library，无覆盖率工具 |

顺带修正第三处过期文档：`AGENTS.md` 称"测试集中在 `handler/test_test.go`"——该文件并不存在。

### 5.2 分层目标

100% 是目标，但**覆盖率的边际价值在不同层差异极大**。建议分层执行，而非一个全局阈值：

| 层 | 范围 | 阈值 | 理由 |
|---|---|---|---|
| **A（硬门禁）** | Go 纯逻辑：`quota/contract.go`、`quota/http.go` 的 `pick`/`fill`/`map`、`service/stats.go` 的聚合原语（分桶、分位、分组、错误分类、成本）、`balancers/`、`common/` 分页；前端 `lib/**` + `**/utils/**` + hooks + stores | **100%** | 纯函数、无 IO、边界即契约。这里是 100% 真正划算的地方——每个分支都对应一个真实的语义决定（"三者任意两个"、分位数的秩定义、成功率排除 running），漏测就是漏语义 |
| **B（高覆盖）** | `handler/**`、`service/**` 请求路径、`quota/**` 编排与缓存 | **≥90%** | `httptest` + 内存 SQLite 可测，但错误路径注入成本高，强求 100% 会写出一堆为凑数的测试 |
| **C（行为覆盖）** | React 组件与页面 | 不设百分比；**每个视图必须覆盖 加载 / 空 / 错误 / 有数据 四态** | 对展示型组件追 100% 会催生断言实现细节的脆性测试，是负收益 |

**另加一条比阈值更有用的门禁**：**新代码不得使覆盖率下降**。这比一个写死的数字更能防倒退。

> 组件层若坚持也要 100%，可以做，但请预期这部分测试的维护成本高于它提供的保护。这是我唯一在方案里往回推的一点，其余全按你说的办。

### 5.3 设施

**Go**

```bash
go test ./... -coverprofile=cover.out
go tool cover -func=cover.out | tail -1      # 总覆盖率
go tool cover -html=cover.out -o cover.html  # 人工看缺口
```

- A 层各包单独门禁，脚本断言 `-cover` 输出为 `100.0%`
- handler 测试用 `httptest.NewRecorder()` + `gin.CreateTestContext()`；DB 用 `file::memory:` + `AutoMigrate`，**每例建库**避免串扰
- 新增 `service/stats_test.go` 用**表驱动**覆盖：分桶阶梯每个档位、分位数的最近秩定义、成功率分母排除 `running`、错误分类七类的首匹配顺序、单位换算
- `main` 包（`router.Run`）与文件系统初始化**不进 100% 门禁**——实际不可测，硬凑只会写出无意义测试

**前端（从零搭建）**

新增：`vitest` · `@vitest/coverage-v8` · `jsdom` · `@testing-library/react` · `@testing-library/user-event` · `@testing-library/jest-dom`

- `webui/vitest.config.ts`：`environment: 'jsdom'`，复用 `vite.config.ts` 的 `@` 别名；覆盖率 `thresholds` 用 glob 精确圈定 A 层路径
- `lib/api.ts` 用 mock `fetch` 覆盖：401 跳转登录、`body.code !== 200` 抛错、分页解包
- `package.json` 增 `test` / `test:coverage`，与 `lint`/`build` 同级

**把设计约束也变成测试** — 这条值得单独做：

`.impeccable.md` 里的色板是**算出来的**（跑过验证器的），那就让它可回归：

- 新增测试固化 8 槽 hex 与 `.impeccable.md` 一致
- 把 dataviz 的 `validate_palette.js` 接为构建期检查（或把六项检查移植进 vitest）——**色板一改就红**
- 同类断言还可加：组件树中不出现 `border-left-width > 1px` 的强调条、不出现双轴图表配置

这比"记得不要用侧边条纹"的口头约定可靠得多。

### 5.4 与实施顺序的关系

测试随步骤走，不留到最后：

- **S1** 交付即含 A 层 100%（stats 聚合原语）
- **S2** 交付即建立前端 vitest 设施 + 色板回归测试
- **S3 / S4 / S6** 每页按 C 层四态写测试
- **S5** 交付即含 `quota/contract.go` 的 100% 表驱动测试——**这是全项目最该 100% 的一个文件**（宽松归一契约的每个别名与边界都是行为承诺）

---

## 6. 实施顺序

分 6 步，每步可独立验收、可合并。**全部在 `feat/console-redesign` 分支上进行**（D10），每步一个提交或一组聚焦提交。

| 步 | 内容 | 产出 | 测试门禁（§5） | 依赖 |
|---|---|---|---|---|
| **S1** | 后端聚合引擎：`service/stats.go` + `handler/stats.go` + ChatLog 复合索引 | `/api/metrics/stats` 可用，旧 metrics 端点转调 | A 层（stats 原语）**100%** · handler ≥90% | 无 |
| **S2** | 设计系统落地：主题机制修复（`data-theme` 三态）+ 新 token + 自托管字体 + 补齐 Radix 原语 | 壳与 token 就位，页面尚未重画 | 搭起 vitest 设施 + **色板回归测试** | 无（可与 S1 并行） |
| **S3** | 新壳与导航（观察/配置分组）+ 总览页 + 分析页 | 核心观察路径可用 | 各页四态覆盖 | S1、S2 |
| **S4** | 日志 / 请求内容 / 对比三页重做（含图表修正清单） | 排障路径可用 | 各页四态覆盖 | S2（S1 用于日志筛选对齐） |
| **S5** | 配额后端（契约→内置→HTTP→沙箱→两个内置适配器）+ 配额页 | 配额功能完全落地 | `quota/contract.go` **100%** | S2 |
| **S6** | 配置类页面重做 + 巨型文件拆分 + 文档修正 + 存档 dashboard | 收尾 | 各页四态覆盖 | S3、S4 |

**起手**：S1 + S2 并行。S2 的主题机制修复是**所有页面的前置**——不改它，新 token 无处安放；S1 是纯后端、无依赖，且一旦就位就能对照 dashboard 的真实输出逐字段验证聚合正确性——**在动前端之前先把数字对齐**，能避开"图对了但数错了"这类最难查的问题。此外 S1 的聚合原语正好落在 A 层 100% 覆盖范围内，起手就能把测试基线立起来。

**提交规范**：沿用 `AGENTS.md` 的 Conventional Commits（如 `feat(api): 新增聚合统计端点`）。每次提交前跑 `go test ./...`（前端就位后加 `pnpm run test`）。


---

## 7. 风险与未决

| # | 风险 | 影响 | 建议 |
|---|---|---|---|
| R1 | **goja 宿主 API 边界**（3.4） | 高 | 采纳方案 B：两个复杂脚本升为 Go 内置，沙箱面积保持极小 |
| R2 | 聚合语义与 dashboard 不一致 | 中 | S1 完成后用同一批日志跑 dashboard 与原 Go 实现做**逐字段对拍**，尤其成功率分母、分位数定义、单位换算 |
| R3 | 重设范围大（10 个页面） | 中 | 严格按 S2→S3→S4 顺序，壳与 token 先冻结再逐页迁，避免返工 |
| R4 | 现有前端无测试基础设施 | 中 | S2 先搭 vitest；A 层 100% 硬门禁，B 层 ≥90%，C 层四态覆盖（§5） |
| R5 | 配额配置含明文凭据 | 中 | 沿用 0600 权限 + gitignore；文档明确"写权限 ≈ 密钥可读" |
| R6 | 主题机制改动影响现有全部页面 | 中 | 改 `data-theme` 时一次性扫掉所有 `.dark` 用法，别留两套并存 |
| R7 | 字体自托管增加体积与构建步骤 | 低 | 子集化只保留所需字符集；中文走系统栈不入包 |
| R8 | goja 为新增第三方依赖 | 低 | 纯 Go 无 cgo，保 `CGO_ENABLED=0`；宿主 API 仅三个，攻击面小 |

**全部未决项已拍板**

| # | 原问题 | 结论 |
|---|---|---|
| 1 | D1 与 D4 的口径 | **完全重新设计**（D4）。现有布局、信息架构、视觉均不预设沿用 |
| 2 | R1 沙箱方案 | **采纳 B**：scnet/opencode 升为 Go 内置适配器，goja 只服务长尾（§3.4） |
| 3 | hero figure 选哪个数 | 选**当前在途请求数**；单人自用影响小，后续可改 |
| 4 | 配额配置落点 | **`./db/quota.config.json`**，可用 `LLMIO_QUOTA_CONFIG` 覆盖 |
| 5 | 分支 | **`feat/console-redesign`**（已建） |
| 6 | 测试覆盖 | **目标 100%，分层执行**（§5）：A 层 100% 硬门禁 · B 层 ≥90% · C 层四态覆盖 |

---

## 8. 验收标准

**功能等价性**
- 1.4 节 24 项能力逐项可用
- 聚合端点与 dashboard 在相同时间窗、相同数据下**逐字段一致**（R2 对拍）
- 配额三类数据源均可配置、试跑、保存、刷新；两个内置适配器（scdeepseek/moonshot 类）与两个登录型源（scnet/opencode）均返回正确余量

**设计规范**
- 色板与 `.impeccable.md` 一致；任何新图表色**重跑验证器**
- 无侧边条纹、无渐变文字、无双轴、无 >8 系列、无手挑色
- 每个图表有 hover/focus 层与表格视图
- 深浅两套主题逐页目视验收

**无障碍（D9）**
- 正文对比度 ≥4.5:1，大字 ≥3:1
- 图表序列不只靠颜色区分；状态不只靠红/绿
- 全流程键盘可完成：导航、筛选、表格、图表读值、模型排序拖拽
- **任意断点下无文字被裁切、遮挡或压住**

**交付**
- `CGO_ENABLED=0` 单二进制构建通过，前端已 embed
- `go test ./...` 通过；`pnpm run lint` 与 `pnpm run build` 通过
- `CLAUDE.md` 中 SWR 与 `handler/home.go` 两处描述已更正
- `llmio_dashboard` 已存档
