# llmio_dashboard → llmio 合并与整站重设计方案

> 状态：已评审 · 进行中（第二轮分支 `feat/console-iter2`；第一轮 `feat/console-redesign` 停在 `v0.8.18`）
> 拟稿 2026-09-29 · 最后更新 2026-10-01
> 目标：把 `E:\Projects\llmio_dashboard` 的能力并入 `E:\Projects\llmio`，**同时把整个管理台重新设计**——重设即合并方案。
> 前置阅读：`.impeccable.md`（设计上下文与已验证色板）

## 进度

| 步 | 状态 | 提交 |
|---|---|---|
| S1 后端聚合引擎 | ✅ 完成 | `8ab0687` · `810775b` |
| S1.5 分时段计费（peak） | ✅ 完成 | `6ef1b25` |
| S2 设计系统落地 | ✅ 完成 | `635caa6` · `c8d6f7d` |
| S3 新壳与导航 + 总览页 + 分析页 | ✅ 完成 | `79fee98` · `3ba076f` · `7ff0e95` · `2f70184` · `d780614` |
| S4 日志 / 对比 | 🟡 **大部分完成**：日志页 + 对比页 + 请求内容页已重做；**缓存前缀分析待补 tools 维度以外的细节** | `d5822ad` |
| S5 配额后端 + 配额页 | ✅ 完成 | `cbbdc01` · `3a922ea` · `52244c0` |
| S6 配置类页面 + 收尾 | ✅ 完成（**偏差**：`log-chat` / `login` / `layout` / `quickstart` 仍无 C 层四态测试，`logs` 的静默刷新分支未钉；`llmio_dashboard` 存档留给用户。`home` / `logs` / `compare` 的缺口已在收尾后补上，见「收尾交出去之后又补的一轮」） | `bad484a` · `384aea7` · `34563da` · `0be607d` · `d77dc3d` · `29e4cb6` · `bb1cf0b` · `8083e56` · `e41ed61` · `577df1d` · `6e2db67` · `4a89d1d` · `a77afa7` · `f5b6e78` · `fc9c38a` · `d681d6f` · `126da62` |
| 第二轮（`feat/console-iter2`）用户提的六条 + 峰谷计费入口 + 前后端功能对照表 | ✅ 完成 | `caee842` · `77e235d` · `c0d3e55` · `2ae7042` · `58af01d` · `403dbc0` · `3911282` · `8380ae6` · `4fbd719` · `14f8086` |

仓库状态（2026-09-30 整理历史）：

- `master` 现在是**上游原样**：`atopos31/llmio` 的 `aaa65fe`（= 上游标签 `v0.8.16`）。此前 fork 把三处上游补丁在本地重写了一遍再合并上游，图上因此留下三条重复提交与两个合并气泡。逐项核对内容后（两棵树只差上游自己的 `32ce583`，无任何 fork 独有内容）把 master 对齐上游，并删掉 fork 自造的 `v1.0.1` 标签与两个「上游已合并、fork 还在」的分支（`feat/session-header-injection`、`fix/accept-encoding-transparent-decompress`）
- 本分支已 rebase 到干净的 master，26 笔。改写带来的**唯一内容变化**是换上了上游的会话头注入（旧基线 `1f6fc3d` 缺这一版，也就是说改写前的分支反而比 master 少一个已合并的特性）；分支自己的改动逐项核对无丢失，`go build` / `go vet` / `go test ./...` 全绿
- 进度表里的提交号已随这次改写更新。**提交正文里不写哈希**——历史一改写必然失准，需要留痕的提交号只写进本表
- **今后**：master 即上游基线，不再在本地重写上游补丁。上游最后提交为 2026-09-29（`aaa65fe`）
- 残留一处删不掉：GitHub 的 `refs/pull/1/head` 仍指向旧提交 `de2bccf`（PR 引用不随分支删除而消失），`git ls-remote` 里看得到，但不进任何分支的图

S1 交付内容与偏差：

- 新增 `GET /api/metrics/stats`（单端点：趋势 / 五维下钻 / 延迟 / 错误 / 排行榜）与 `GET /api/metrics/granularities`
- `service/stats.go` 全部 30 个函数语句覆盖 **100%**；`handler/stats.go` 同理
- ChatLog 补 4 个索引（含 3 个复合），并展开 `gorm.Model` 以便给 `created_at` 打复合标签
- 顺带修掉一处既有问题：`models` 的测试未关闭 SQLite 连接，导致 `go test ./...` 在 Windows 上恒为红（上游 `32ce583`）。分支上曾有一版逐字相同的本地修复，整理历史时被 git 识别为重复而丢弃，改以上游那版为基线（见「仓库状态」）
- **偏差**：既有的 `/api/metrics/use|counts|projects` 未按原计划改为转调新引擎。它们带 `topN=5` + `"others"` 塌缩，直接转调会改变响应结构并打断现有前端；留待 S3/S4 页面迁移后一并退役

S3 已完成的部分与实测结果：

- 导航按「观察 / 配置」分组；修正活跃态匹配（原先 `/logs/5/chat-io` 不高亮任何项）
- 三态主题切换接入顶栏，桌面端默认展开侧边栏
- **修正一处真实缺陷**：`App.tsx` 曾覆盖 `storageKey`，与 `index.html` 首屏脚本读取的键不一致
- 浏览器实测（Go 二进制 + Chrome）：三态切换、深色 token、自托管字体、嵌套路由高亮、刷新保持主题、无闪烁，全部符合预期
- 新增设计契约测试：色板 hex、深色两块一致、禁项、字体自托管、焦点环、reduced-motion、文档与代码一致。前端测试 101 个，纯逻辑层覆盖率 100%

**S3 已完成**（`3ba076f`）：总览页重做，含 4.4 节图表修正清单的主体——

- 双轴拆成两张单轴图（请求数与 Token 各一张）
- 环形图改列表（错误类别：要比较的量，环形对接近值不可靠）
- 按实体稳定取色（新增 `useEntityColors`，以稳定全集分配槽位）
- 门槛染色改顺序色阶（首包耗时直方图）
- 状态强制「图标 + 文字 + 颜色」三者同现（新增 `StatusMark`）
- 浏览器实测：零横向溢出、图表填充确认为验证过色板且深浅自动切换、实测对比度全部 >=4.5:1

顺带修掉两处后端语义缺陷（均由真实数据暴露）：`retryRate` 出现 106.7% 这种非法的
「率」（实为平均每请求重试次数，已改名 `avgRetries`）；`byKey` 未按名称合并同名分组。
另为 Token 构成图补了 `TrendPoint.cached`。

**S3 已完成**（`7ff0e95`）：分析页 `/analytics`——§4.3「分析」一节落地（一行筛选 · 四个
维度视图 · 五维下钻表）。数据全部由已就绪的 `/api/metrics/stats` 一次返回，因此这一页
只做"选择与呈现"，不做任何重新聚合。

- 两个筛选取向都有明确取舍，且都写进了测试：**筛选选项从一份未加筛选的 `universe`
  切片派生**（否则筛了 A 之后其余选项消失，用户再也切不到 B，只能先清除筛选）；
  **分桶档位只有服务端一个真相来源**（拿不到就不显示控件，不退回本地写死的档位表）
- 时间范围是「预设行 + 自定义」（§4.3 原文），自定义用原生 `datetime-local`：
  切过去时**用当前预设的窗口预填**（不预填就先面对两个空框），范围非法时
  **不发请求也不清空**已有视图——正在编辑的半截输入不该把页面打成空白。
  输入串按本地时间手工拼而**不用 `toISOString()`**：后者是 UTC，在东八区会造成
  整整 8 小时的静默错位（选 14:00 查了 06:00），这条有专门的用例钉住
- `src/lib/analytics.ts` 归入 A 层 100%：预设窗口的边界口径（今天/昨天按本地日历，
  近 N 天按滚动窗口）、筛选到查询串的映射（密钥传 id 而非展示名）、维度↔筛选字段对应表
- 新增 `Panel` 共享组件：总览页与这一页的"卡片说明"是同一种东西，原位复制会立刻分叉
- **首个按 §5.2 补齐四态（加载/空/错误/有数据）的页面测试**：`analytics.test.tsx` 八项。
  顺带发现加载态此前对读屏等于空白（骨架不可感知，加载与"没有数据"在无障碍树上无法
  区分），补了 `role="status"` + "加载中"

**跑起来才发现的缺陷**：280px（Galaxy Fold 外屏）上「时间范围」与「下钻维度」两个分段
控件比容器宽 15–16px，而外壳是 `overflow-hidden`——不换行即被静默裁掉，最后一个档位
够不着且没有任何提示。两个控件加 `flex-wrap` 后归零（390px 上仍是单行，改动在不需要处
不生效）。这是 A 层与 C 层都覆盖不到的：单测里没有宽度。

**S3 补丁**（`d780614`）：两处一起改，因为它们指向同一个误读。

- **横轴标签按"序列是否跨天"决定要不要带日期**。原来只按桶宽判断（`bucketMs >= 1d`
  才显示日期），于是 1 小时的桶跨三天时轴上只有三遍同样的 `HH:MM`，分不清哪段是哪天
  （用户反馈的原话是"只有重复的小时段，而不显示时间，我觉得很难看"）。判断收进
  `bucketLabelFormatter(data)` 这个工厂，三个调用点（请求趋势、Token 趋势、失败趋势）
  就无法各自漏掉它——原来那三处各写一句同样的调用，缺的是同一件事。
  单点/空序列不判跨天；`src/lib/format.ts` 因此**纳入 A 层 100% 门禁**：
  它现在持有的是语义决定，判断错了页面照样渲染。
- **时间序列补到请求窗口的两端**（原「待定」项，已决）：只补观测区间时，X 轴虽是分类轴、
  相邻两点在图上等距，实际相隔多久读不出来——"三天里只跑了两小时"被画得跟"三天一直在跑"
  一样满。现在由服务端补（`fillTrendWindow`，`/api/metrics/stats` 单端点，前端无需改动）。
  两条边界：**扫描触顶时刻意不补**（日志按时间倒序截断，窗内老年份根本没被读到，补零等于把
  "没看"伪造成"没有请求"，与上一条恰好是同一个错误的反方向）；**空序列不补**（没有请求时
  补出一整窗的 0 只是白白撑大响应，页面本来就显示空状态而不画图）。补齐另有 10000 桶的预算：
  窗口长度来自查询参数，`from=0` 配 `granularity=5m` 能算出近 600 万个空桶。

**S4 已完成**（`d5822ad`）：日志页与对比页重做。

- 日志页：去掉双实现（原为表格 + 卡片列表两套）、冻结 ID 列、状态改用图标+文字+颜色、
  详情由弹窗改右侧抽屉、跨页多选对比、自动刷新（隐藏时跳过、回前台补一次）
- 对比页：**按缓存真实构造顺序 tools → system → messages 计算公共前缀**。
  原实现只比较 messages，而工具定义通常在最前面、体量最大——
  两个请求共享同一份 tools 时会误报前缀为零，把缓存命中的原因判断错
- 比较是字节忠实的：消除缩进噪音但不排序键（缓存看到的也是原始键序）

真实数据实测：公共前缀 5 项（工具 2 · 系统 1 · 消息 2），四条请求的起分叉处各不相同，
其中一条被正确识别为「完整包含公共前缀」。

**S5 已完成的部分**（`cbbdc01` · `3a922ea`）：

- `quota/contract.go` — 归一契约 + 格式模板 DSL，**语句覆盖 100%**
- `quota/jsonextract.go` — 模糊 JSON 抽取（围栏 → 整体 → 字符串感知平衡括号 + 候选打分），**100%**
- `quota/sandbox.go` / `sandbox_child.go` — goja 子进程沙箱。宿主 API 三个（`console` · `env` · `output`）+ 可选 `fetch`（禁内网、限体积、重定向逐跳重校验）；无 require / 文件系统 / 子进程 / 定时器
- `quota/http.go` — HTTP 适配器（占位符插值 · 四型鉴权 · 点路径取值 · 字段映射 · constants），**100%**
- `quota/builtin.go` — 内置适配器注册表 + deepseek / moonshot / custom 解析，**100%**
- `quota/scnet.go`、`quota/opencode.go` — 两个登录型适配器（§3.4 方案 B 的落点），97.2% / **100%**
- `quota/config.go` — 配置读写（原子写 · 0600）· 类型归一 · 脱敏 · 密钥回填 · 校验，99.2%
- `service/quota.go` — 编排层：QuotaStore（配置 CRUD · 缓存 · 并发扇出 · 每源独立刷新）·
  试跑 · 从上游供应商发现与导入，**97.4%**
- `handler/quota.go` — 八个端点（读配置 · 增删改源 · 跑全部/刷单个 · 试跑 · 发现 · 导入），
  **100%**；`main.go` 接线并在 `init()` 最前面识别沙箱子命令

`service/quota.go` 里几处值得记的语义决定：

- **只缓存成功的结果**。失败常是瞬时的（网络抖动、上游 5xx），把它缓存会把一次抖动
  放大成整个 TTL 窗口的持续报错。
- **缓存下限 10s**（`MinCacheTTL`）。即便用户把刷新间隔调到 1s，也不真的每秒打一遍上游。
- **"最差"先比状态序再比百分比**。为跨源比较，把契约的 `statusRank` 导出为
  `StatusRank`——在编排层另维护一份顺序表必然漂移。未识别状态按 `unknown` 计而非排最后，
  否则它会盖过真实的 `exhausted`。
- **`fetchRows` 返回 `any` 而不是先摊成行**。契约的 `Normalize` 本就接受任意形状，
  先摊一层是多余的，且会丢掉"原始产出"——试跑面板正需要原样回显它。
- **`TestMain` 是脚本路径的硬要求**。`RunScript` 是 re-exec 当前可执行文件；
  在测试里那就是测试二进制，因此 `service` 包也必须识别 `SandboxCommand`
  （`service/quota_sandbox_test.go`）。这同时验证了生产 `main()` 里那条接线是对的。

`handler/quota.go` 里值得记的：

- **只读开关降级为 `LLMIO_QUOTA_ALLOW_WRITE`**。原设计是"仅回环 + 显式开启"，
  理由是"写权限 = 可在主机上执行任意命令"；goja 沙箱替换 spawn 之后这个前提消失，
  且整个 `/api` 已在 TOKEN 之后，所以降成一个开关而不是保留那套限制。
- **试跑在只读模式下仍然开放**。它是配额功能最有价值的作者循环（改脚本 → 立刻看
  解析结果与原始产出），且不改变任何状态，因此不受写保护。
- **导入只接受 `upstreamId`**，密钥由服务端从上游配置直接取，不经过浏览器。
  这条在同源之后依然显式保持。
- **400 与 500 的分界靠错误文本**（`isQuotaInputError`）：校验类错误措辞是既定的中文
  提示、最终要原样展示给用户，因此不引入哨兵错误。

**修掉一处真实缺陷**：`UpsertSource` 原先在**归一之前**就把 `incoming.ID` 用掉了，
因此"不带 id 新增"时会带着空 id 走到后面两步——`MaskSource` 会把空字符串打码成
`****` 落盘，`Invalidate("")` 的语义是"清空整个缓存"，于是每次新增都白清一次全部缓存。
现在先归一 id 再落盘。

覆盖率口径：本项目的门禁按**单包**统计（`go test ./pkg/ -cover`），与 S1 对
`service/stats.go` 的口径一致。跨包口径（`-coverpkg=./...`）会把未被本包测试触达的
标准库语句也计入分母，数字没有意义，不作为依据。

配额包整体语句覆盖 **98.4%**；A 层（`contract.go` / `jsonextract.go` / `http.go`）与
`builtin.go` / `opencode.go` 均为 **100%**。未覆盖的少量分支集中在 `sandbox_child.go` 的
`rand.Read` 失败路径、`scnet.go` 的同类防御分支、`config.go` 的 `MkdirAll` 失败——
结构性不可达或需要权限受限环境，不硬造测试。

**`scriptstore.go` 不再需要（一处真实简化）**：原实现把在线编辑的脚本落成
node/python 等**可执行文件**，运行时映射到解释器（`scriptStore.js` 的
LANGS / resolveCommand / writeTemp）。§3.4 定下「goja 替换 spawn」后这层整个消失：
只有 JavaScript 一种语言、源码内联在配置 JSON 里可手工编辑、试跑把源码**直接**
交给沙箱（原实现要写临时文件再删）。所以计划 §3.1 里列的 `scriptstore.go` 是
被这个决定消掉的，不是漏了。

沙箱的两处实现要点：

- **必须 re-exec 自身**做进程边界：goja 无堆上限，进程内一个 `while(true) arr.push()` 会把网关拖垮。
  子命令 `__quota_script_sandbox` 在 `main()` 最前面识别
- **同进程测试会死锁**：`SandboxMain` 原先直接绑 `os.Stdin/os.Stdout`，测试替换标准流后父进程
  自己持有管道写端，`io.ReadAll` 永远等不到 EOF。已改为核心逻辑 `RunSandbox(in, out)` 显式传 io，
  测试用 `bytes.Buffer`，`SandboxMain` 只做薄包装

`http.go` 相对原 JS 版的两处**有意偏差**：

1. **契约同时接受 `[]map[string]any`**。原 JS 只有一种数组形态；Go 侧适配器构造的行天然是
   `[]map[string]any`，而 JSON 反序列化得到 `[]any`。只认后者会让"上游 JSON 能归一、适配器产出
   反而不能"——端到端接起来时踩到过
2. **`map` 为空时整行透传**给契约。契约的意义就是认得 `used/已用/usage` 这一类别名；
   对端本就返回接近契约形状的结构（自建网关常见）时，写一份 `map` 是多余的。
   真认不出时契约仍会明确报错，不会静默

（`toStr` 保持既有语义不变：只处理标量，嵌套容器归为空串。这条有既有测试锚定，不改。）

**scnet 的 RSA-512 问题（值得记一笔）**：scnet 的 SSO 登录要求用 RSA-512 公钥
加密口令，而 **Go 1.24 起 `crypto/rsa` 对小于 1024 位的密钥一律拒绝**
（`GODEBUG=rsa1024min=0` 可放开）。公钥由上游写死，改不了。两条路里选了后者：

- 设 GODEBUG：进程级全局状态，等于为一个数据源降低整个程序的安全策略，
  且必须在任何 rsa 调用之前设置，容易在别处被覆盖
- **自己实现 PKCS#1 v1.5 公钥加密**（`rsaEncryptPKCS1v15`）：公钥加密就是一个
  模幂 `c = m^e mod n`，不涉及私钥侧的常量时间要求，用 `math/big` 实现既正确又无全局副作用

正确性由 `TestRSAEncryptPKCS1v15MatchesStdlib` 对拍守住：自造 512 位密钥对，
本实现加密后由标准库私钥解密能还原原文，密文长度一致；真实 scnet 公钥产出
64 字节密文 / 88 字符 base64，与 Node 原实现一致。

**opencode 的会话裁剪**：Cookie 里只有 `__Host-console_session` 是必需的。
`auth=`（Fe26.2**…）带签名有效期，过期后反而干扰判断，因此默认不发。
用户可贴完整 Cookie 串、裸 `st_xxx`、或裸 uuid，三种写法都认。

**相对原 JS 版的另一处偏差**：scnet 不做会话 cookie 落盘复用。每源刷新间隔以分钟计，
每次完整登录（3~4 个请求）完全可接受，换来的是没有会话文件的过期判定与清理问题。

**S5 配额页**（`52244c0`）：§4.3「配额」一节的八项要求全部落地（摘要条 · 卡片网格 ·
四样式 · 三态编辑器 · 试跑面板 · 单条自定义 + token 芯片 · 上游导入 · 只读提示）。

- `src/lib/quota.ts` 是 Go 侧 `RenderItem` / `DefaultFormat` 的**镜像**，
  并归入 A 层 100% 覆盖。它存在的理由只有一个：尚未保存的自定义格式要在前端实时预览；
  已保存条目的展示文本仍以服务端下发的 `text` 为准
- 8 个新 UI 原语里只装了真正需要 Radix 语义的 6 个，`meter` / `skeleton` 手搓。
  产物上兑现了：配额 chunk 92KB，**没有**把 409KB 的 `charts-vendor` 拉进本页加载路径
  （这正是 §4.4 把 `ring` 从 ECharts 仪表盘改成 meter 的附带收益）
- 顺带补了 §4.4 的两条通用要求：图表样式的切换是 `ToggleGroup`（切的是同一视图的参数，
  不是换一屏内容）；卡片的次要操作保留 hover 显隐，但键盘 focus 等效触发

**跑起来才发现的三个缺陷**（静态检查与单测都过不了这一关）：

1. **失败数据源白屏**。Go 侧失败时 `items` 是 nil slice，序列化成 `"items": null`，
   而前端类型声称 `QuotaItem[]`——卡片在 `.filter` 上崩掉，整页白屏。
   改为在真实类型上承认可空，并用一个纯函数做归一（`itemsOf`），补了测试
2. **关闭对话框后整页点不动**。点开「数据源」下拉菜单、再点其中一项，关掉对话框后
   body 残留 `pointer-events: none`。根因是 `@radix-ui/react-dismissable-layer` 被装了
   **两份**：react-dialog 1.1.14 把它精确锁在 1.1.10，react-menu 用 1.1.19。该包里
   `originalBodyPointerEvents` 是**模块级变量**，而"已禁用外部指针的层"这个集合挂在
   各自包的 Context 上——两份拷贝既不共享集合、又共享变量：菜单先开记下 `""`，
   对话框再开时在 body 已是 none 的情况下记下 `none`，于是关闭时按自己那份快照把
   `none` 写回去。`react-remove-scroll` 是同一类问题（模块级 `idCounter` / `lockStack`，
   两份会让上方那份误判自己是最后一个滚动锁）。在 `pnpm-workspace.yaml` 里 override 成
   单一版本根治
3. **默认格式在缺 total 时的脏输出**。见上「后端补齐」第三条——这条是 TS 镜像的测试
   先报出来的（默认模板渲染成 `42 /  tokens`），回头发现 Go 侧同样如此

**一处流程教训**：前两次浏览器验证之所以"修了还是坏"，是因为 Go 用 `//go:embed webui/dist`
在**编译期**内嵌前端产物——改了前端只重建 dist 而不重新编译二进制，测到的仍是旧包。
验证前端改动必须先 `go build` 再重启，或直接用 vite dev server。

**i18n 的一处真实缺口**：`card.hidden_items` 三语都缺，顶层却有个同名的孤儿键
（从未被读到）。徽标因此只靠 `defaultValue` 兜底，三语都显示英文。已把键挪进 `card` 段。

---

**S6 交付内容与偏差**（`bad484a` · `384aea7` · `34563da` · `0be607d` · `d77dc3d` · `29e4cb6`
· `bb1cf0b` · `8083e56` · `e41ed61` · `577df1d` · `6e2db67` · `4a89d1d` · `a77afa7` ·
`f5b6e78` · `fc9c38a` · `d681d6f`；收尾后补的 `126da62` 见本节末尾）：

**拆巨型文件**（先钉行为、再拆，两步分开提交）

- `routes/model-providers.tsx` 1521 → 745 行，按"谁拥有数据"拆成 9 个子文件：页面留着
  models / providers / 关联列表与筛选，子组件只画。拆之前先落了九条行为基线——
  其中「错误」一组**刻意留空**，因为当时那一页对取数失败只弹 toast、正文照旧说
  「暂无可关联模型」；先让测试如实记下现状，不把待修的缺陷伪装成已覆盖
- 拆出的两处易被顺手改坏的地方已写进代码注释：模型列表与关联列表都是桌面表格 +
  手机卡片两套并行实现；新建的模型仍要按展示顺序落回 `models`

**一处贯穿四页的真实缺陷：失败装成空态**

`auth-keys` / `providers` / `model-providers` 三页取数失败时只弹一条会自己消失的提示，
正文照旧写「暂无数据」——把"没取到"说成了"这里本来就没有"。修法统一：失败替换面板
内容，给出**原文**（原文才可能指向原因）与重试入口。空态与失败态随后合成
`components/state-views.tsx` 一份，"失败必须给原文"由此是代码里的一条规矩，而不是
每页各自记得。

`config` 页是同一缺陷的第三种形态，但**修法必须不同**：卡上显示的是默认值，编辑与
保存都不依赖这次读取，整页换成失败态等于把可用页面收走。因此失败只在顶部作提示，
但要说清"下面显示的可能不是已保存的值，请先重试读取"。清理历史对话框同理——表格
里的「暂无数据」是对服务端事实的断言，不该由一次失败的请求替它说。

**键盘与无障碍**（对应 §8 的两条验收）

- 模型排序补键盘路径：焦点在行上时 Alt+↑/↓ 移一位、就地保存，走与拖拽**完全相同**
  的保存路径。用 Alt 而不是裸方向键（方向键在表格里是行内浏览）；筛选态下与拖拽
  一样禁用，并且**说出原因**（带着筛选移会把局部顺序当成全量顺序存下去）；移到
  尽头时说明"已在开头/结尾"而不是静默；结果用 `aria-live` 播报，按模型 ID 把焦点
  找回原位（挪动节点在部分浏览器里会丢焦点）
- 状态不再只靠颜色：关联列表的绿/红小格（含义原本只挂在 `title` 上，键盘与读屏都
  够不着）改为装饰 + 一行计数「成功 N/M」；能力列的两个无名 ✓/✗ 补 `role="img"` +
  已翻译的 aria-label；有效期过期除变红外补「已过期」；三处图标按钮与两个开关补
  aria-label；删除确认对话框原先硬编码中文（en / zh-TW 用户看到中文）改走 i18n，
  桌面与手机两份重复内容收成一份
- 加载态：`Loading` 声明 `role="status" aria-busy`（骨架与转圈在无障碍树上等于空白，
  不声明则"正在加载"与"没有数据"对读屏是同一件事）；密钥页与配置页加载态改列表骨架
- 失败态/状态色的固定色号（gray-500 / red-500 / emerald-100 / green-800 等）全部换成
  语义 token，深色主题下才有对比度

**键鼠之外的收尾**

- 退役三个旧 metrics 端点：`/api/metrics/use|counts|projects` 与 `handler/home.go`
  （它只做这三件事，从不做静态服务——静态是 `main.go` 的 `//go:embed`），以及前端
  只用在这三处的 `getMetrics` / `getModelCounts` / `getProjectCounts`、四个图表组件、
  从未被渲染的 613 行旧模型页 `routes/models.tsx`
- `handler/` 此前零测试，补上第一组（`test_test.go`）：假上游记录收到的请求头，断言
  自定义头送达、api_key 覆盖同名自定义头的次序、`with_header` 开关两种透传、非 OpenAI
  类型被拒绝。这组断言**在改动前失败**（`X-Opencode-Session` 为空），改的是真缺陷：
  能力测试自建 SDK 客户端时漏了 `with_header` / `customer_headers` 与代理商代理，
  于是配了自定义头的上游（如 opencode）测试必然失败、配了代理的提供商会绕过代理
- 死代码：删掉从未被 import 过的 `ui/scroll-area.tsx`（连同 `@radix-ui/react-scroll-area`
  与 lockfile 条目）；密钥页与模型路由页逐字重复的 `MobileInfoItem` 提成共用组件
  （密钥页那份多出的 `mono` 参数从未被传过）
- `pnpm run lint` 归零，且不是整体关规则：`ui/` 只点名放行 shadcn 按设计同时导出的
  三个常量，`main.tsx` 单独关（入口不导出组件）；密钥页取数收进 `useCallback`，
  依赖项就是查询条件
- `CLAUDE.md` 两处与代码不符的说法已改：handler 清单去掉 `home.go` 并说明静态文件由
  `main.go` 的 `go:embed` 提供；前端技术栈删掉不存在的 "SWR for data fetching"
  （取数是 `api.ts` 的 fetch + 页面自己的 `useState`/`useEffect`）
- 三个只有标题没有说明文字的对话框（日志清理 / 配额设置 / 关联表单）显式
  `aria-describedby={undefined}` 表明是有意的，不再让 Radix 一直告警

**额度页的遗留也一并清掉**（`d681d6f`，`state-views.tsx` 的注释里点过名）

加载中的骨架没有 `role="status"`；取数失败被塞进空态的 `hint` 里（无原文、无重试）；
摘要条"最紧张"那条的状态是个只换颜色的小圆点。三处分别改为声明 `role="status"`、
改用 `ErrorState`（重试走 `force`，因为失败很可能来自缓存下来的上一轮结果）、改用
卡片那枚"字形 + 文字"的 `QuotaStatusBadge`。

这一页此前没有页面级测试，新加 `quota.test.tsx` 按四态钉住上面三条。**三条钉子都做过
反证**：分别去掉 `role="status"`、把徽标换回小圆点、把 `ErrorState` 换回 `EmptyState`，
对应用例各红一次，还原后全绿。

**收尾交出去之后又补的一轮**（`126da62`）

上面那节"C 层现状"写下的偏差里，`home` / `logs` / `compare` 三页无测试这件事没有就此搁置。
补测试时先按**实际缺陷**而不是按覆盖率数字分诊，结果前两页确实是同一处缺陷的第六、七例：
总览页失败只弹一条会自己消失的 toast（`stats` 仍为 null → `hasData` 为假 → 显示"该时间范围
内没有请求"），日志页失败则照旧显示"暂无请求日志"——都在替服务端断言一件失败的请求没有资格
断言的事。两页按已有做法修（无行时 `ErrorState` 顶掉内容，有行时保留旧行 + "这次没取到，
下面是上一次的结果"横幅），加载态补 `role="status"` 与 `sr-only` 文案。对比页不需要这笔修改——
它的失败本就是逐条的（`allSettled` + 卡片内原文），只补了加载声明；`log-chat` 复核后确认本来
就合规，未动。顺带：`home` 的 `load(silent)` 参数已无调用点传值，随重写删除；toast 移除后
`home:load_failed` 成了孤儿键，三语一并退役。

三个页面此前都没有页面级测试，一并补上 `home.test.tsx`（7 例）· `logs.test.tsx`（6 例）·
`compare.test.tsx`（3 例）。**新钉的每一条都做了反证**：逐条改动源码（总览页错误态 / 过期横幅 /
加载声明，日志页错误态 / 过期横幅 / 加载声明，对比页加载声明）确认对应测试转红后还原。第八次
反证——日志页静默刷新分支——**没有**测试转红，即该分支并未被覆盖，而这与 `logs.test.tsx` 文件头
注释原先的说法不符；已把注释改成实情，并在文件末尾留一条 `it.todo` 记这个缺口，免得后来者以为
它是被保护着的。

**C 层现状（如实记，`126da62` 之后）**

- 有页面级四态测试：`analytics` · `providers` · `model-providers` · `auth-keys` ·
  `config` · `quota` · `quota-editor` · `home` · `logs` · `compare` · `peak-pricing`
  （26 个测试文件 / 579 例 + 1 条 todo。`peak-pricing` 与 `ui/chart` 是第二轮加的，
  计数随该轮更新——上一轮结束时是 21 个文件 / 456 例）
- **仍无**：`log-chat`（S4）· `login` · `layout` · `quickstart`。§5.4「每页按 C 层四态写测试」
  这条对这四个页面仍未兑完，是收尾**未做**的部分，不是"已覆盖"
- **已知未钉的分支**：`logs.tsx` 的静默（自动刷新）失败分支。要驱动它得先在界面上选中一个
  自动刷新档位，而 Radix 的 `Select` 配上假定时器在这个环境里会卡住（试过，5 秒超时并漏进
  下一条用例）。该分支是原样保留的既有行为，不是这轮新引入的未覆盖
- A 层硬门禁仍为 100%（语句/分支/函数/行）；`lib/api.ts` 按原设计不在 A 层圈定范围内

**CI 与第一个自建 tag**（`e1770fb` · `fa32edb`；tag `v0.8.17`）

仓库原有两条 workflow 都只认 tag，日常提交在合并前没有任何自动检查。补上
`.github/workflows/test.yml`（push / PR / 手动：前端 lint → 单测 + 覆盖率门禁 → 构建 → gofmt
→ go vet → go test → 单二进制构建自检；顺序的理由都写在文件里，其中最要紧的是前端构建必须排在
所有 Go 步骤之前——`//go:embed webui/dist` 缺了产物是编译失败，不是测试失败）。随后给 fork 打了
`v0.8.17`（lightweight，指向 `e1770fb`），两条 tag workflow 照旧发版与推镜像，Release 与镜像均已
成。三件事记下来免得日后重新踩：

- **tag 落在 `e1770fb`，不在分支当前 HEAD 上**。出下一个版本时要重新决定打在哪。
- 推镜像把 Docker Hub 的 `latest` 从仓库里原有的 `1.0.1` 挪到了本构建：版本号更小、代码更新，
  是刻意的结果而不是意外，但 `latest` 的含义因此变了。
- fork 上还留着一个 `1.0.1` 的**草稿** Release（上游时期留下的），未动。

**真机用出来的缺陷：登录型内置没有 env 入口**（`37fb633`；`4b70efc` 是并行跑时的一处假红）

用户报告"内置适配器超算和 op 需要环境变量但是没有提供填写的地方"。顺着查，同一个编辑器上有四处，
都是"编辑器拿不到用户已有的配置"这一个根因的不同切面：**（一）** 登录型内置没有 env 框——脚本
类型一直有，内置没有，于是 scnet / opencode 是"选得出来、存不下去"（后端 `ValidateSource` 要求
这类适配器的 env 非空，用户只看到保存失败，而界面上根本没有可填处）；**（二）** 编辑器拿到的是
卡片上的**取数结果**而不是配置里那份完整的源，而保存是整份替换，于是每次编辑都会把 url /
timeout / headers / map 清空且没有任何提示——类型系统拦不住（`QuotaSourceResult` 在结构上满足
`QuotaSource`）；**（三）** 原实现按 baseURL 猜内置适配器，猜错就把源指到另一个适配器身上；
**（四）** `parseMapText` 丢掉字面量标记，`unit==CREDITS` 被解析成字段路径后由 `MapRow` 静默丢弃。

前三条的修法：按适配器自带的 `EnvKeys` 渲染 env 框（键名写进占位符）、打开编辑器时按 id 从
`getQuotaConfig` 的配置里取那一份、删掉猜测。新增 `quota-editor.test.tsx`（10 例）钉编辑器的读写
两端，额度页补一条接线断言。五条反证都见到对应测试转红后还原。

**这一轮的真机复核**（另建二进制、独立临时目录、自造 token，未碰仓库那份产物）：25 项全过。测试
里这一段全是打桩的，只有真后端看得见下面两件事，而我第一版探针的期望**两处都写错了**：

- 打码是**部分**打码：`len ≤ 2` → `****`；`≤ 10` → 前 2 位 + `****`；更长 → 前 6 + `****` +
  后 4。env 是按**键名**判断是否敏感的（`SCNET_PASS` 打码，`SCNET_USER` 不打）
- 这个 API 成功与失败**都返回 HTTP 200**，真状态在 body 的 `code` 里。按 HTTP 状态码判断会把
  失败一律读成成功

往返本身是通的：下发打码 → 原样回填 → 保存后磁盘上的真值还原（`real-key-123`、`s3cr3t`），
`url` / `method` / `headers` / `constants` / `map`（含字面量）一字不丢。缺 env 提交时的拒绝原文是
"需要账号会话，请在 env 里配置 SCNET_USER / SCNET_PASS"——这正是界面必须给入口的原因。

**偏差**

- **`llmio_dashboard` 未由我存档**：该目录不是 git 仓库，没有可打的标签也没有可提交
  的历史，存档动作（留档说明 / 冻结路径）留给用户
- 旧 metrics 端点未按 S1 计划"转调新引擎"而是直接退役：重做后已无调用方，转调只会
  留下一个没人用的兼容层
- 配置页刻意不给整页失败态（理由见上），这是与另外三页的**有意不一致**

---

## 第二轮：用户提的六条 + 峰谷计费入口（`feat/console-iter2`）

第一轮交出去之后，用户真用了一天，回来给了七条。值得先记一笔的是这七条的**性质**：
没有一条是"后端能力缺失"，六条是"能力早就在，界面这一层没接对或没接上"，一条是
"接上之后被真机撞出了缺陷"。这与 S1 那一轮（补端点、补索引）是两种不同的工作。

**① 余量不再从上游 llmio 导入**（`403dbc0`）

用户的原话是"兼容问题比较严重"。导入吃的是上游配置的**具体形态**——密钥放在哪个
字段、接口地址能不能从其它字段推出来——而最需要传过来的映射规则恰恰传不过来，
导进来的源往往还得手工改。手工配置 + 试跑本来就覆盖同一条路径，这个入口实际更像
"能点但不好用"的陷阱，因此整条撤掉（服务端 Discover / ImportFromUpstream /
BuildCandidates / BuildImportedSource 与两条路由、前端导入页与三语词条一并退役，
`service/quota.go` 收敛回"纯函数 + QuotaStore"两层）。

附带的好处与配额功能的性质有关：这条通路原本要把上游密钥**从服务端取出、经浏览器
回传**。通路消失后，密钥只剩既有的"掩码展示 + 原样回填"一条约定，少一处明文流转。

**② 趋势提示条印出 NaN:NaN**（`caee842`）

用户报告"请求趋势 / Token 趋势鼠标放上去没有时间，只有 NaN:NaN"。根因在 shadcn
模板的 `ChartTooltipContent` 里取 label 的那三行：**只有字符串 label 才交给
labelFormatter**，其余一律回落到"系列名"。趋势图的 label 是 XAxis 上的时间戳
（数字），于是格式化器收到的是 `t("trend.success")` 得到的"成功"，`Number("成功")`
是 NaN，`new Date(NaN)` 的两个取值方法也都是 NaN，拼出 NaN:NaN。

系列名只该在没有 label 时顶替，改成：labelKey / label 为空才用系列名，字符串照旧查
config 翻译，其余（数字）原样交给格式化器。影响面是**五个挂载点**而不是一个页面
（总览页与分析页各一处请求/Token 趋势、分析页一处失败趋势），三处调用点都是同一句
`label(Number(v))`，所以在包装层修一次就都好了。坐标轴刻度本来就是对的
（tickFormatter 由 Recharts 直接以原始 tick 值调用，不经过这段取值），所以从截图看
只像提示条的问题——这也是它没在第一轮被发现的原因。

**③ 余量条目各自指定画环还是进度条**（`c0d3e55`）

环样式原先写死成"最紧张的那条画环，其余只列文字"：既选不了，剩下的连比例都不画。
现在条目自带样式（回到条目对话框里选）：指定画环的条目画环（**可以不止一条**，横向
排布），其余画进度条，一条都没指定时仍回落到最紧张的那条。

存储格式因此升到 v2，这一处值得单说：v1 里条目上的 `chartStyle` 表达的其实是
**卡片**样式，要提升到源级，否则用户此前设过的值会被静默忽略；v2 起它是真正的条目级，
再往卡片上写就会反过来把卡片样式也改掉。提升时两边留同一个值，升级前后的渲染结果
一致。判断靠 `version` 字段，读出来即写回当前版本，免得每次加载重跑一遍提升。

**④ 请求日志的五个筛选维度改成多选**（`2ae7042`）

模型 / 项目 / 状态 / 类型 / 提供商原先各只能选一个，而看日志时的常见诉求恰恰是
"这几个放在一起看"。

口径与 `/api/metrics/stats` 对齐：这五个维度**逗号分隔即"任一命中"**（splitCSV + IN），
维度之间仍是"与"。三处刻意的例外与取舍：

- `trace_id` / `session_id` / `id` 保持精确匹配——它们是"某一次具体请求"的标识，
  多选没有语义
- `auth_key_id` 走数字解析而不是当字符串塞进 IN：列是整型，隐式转换虽然也判得对，
  但 `"abc"` 会静默变成"查不到"，用户看到空列表而不知道是筛选值不合法
- 旧链接里的 `status=all` 仍按"不过滤"解释。不兼容它的话，一条旧书签会变成
  "筛 `status ∈ {all}`"，得到一个看不出哪里不对的空表

前端把 `MultiSelectFilter` 从 `analytics-filters` 提到 `components/` 下共用（它自己
不翻译，文案由调用方按各自命名空间传入），新增 `lib/logs.ts` 承载 URL 与查询串之间的
往返。URL 上的取值就是逗号拼的那串，页面只认**解析后的那一份**结果——某处若直接读
原始串，就会出现"界面上没筛、请求里却带着 all"这种两处口径不一。

**⑤ 分析页加「模型性能」视图**（`58af01d`）

原来的"下钻"是拿模型当分组**计数**，回答不了"哪个慢、哪个贵、哪个在重试"。新视图把
这些读数摆成一张表并允许按列排序。首列是模型名，它是行的标识而不是读数，因此不进
排序（`MODEL_SORT_KEYS` 取的都是 `GroupStat` 的数值字段，让取值退化成 `g[key]`）。
数字全部取服务端已算好的 `GroupStat`，**前端不新增任何聚合**，只排序与呈现。

两条排序规则值得单说：

- **缺值恒排最后，与方向无关。** `avgTps` / `maxTps` / `avgFirstChunkMs` /
  `p95FirstChunkMs` 的 0 是"没有可用样本"（全失败，或全程非流式），不是一个读数；
  升序时若把 0 排到最前，读出来的正好是"这个模型最快"。这四列单独判缺值，而
  `successRate` 的 0%、`retries` / `cost` / `totalTokens` 的 0 都是真实读数，照常
  参与排序
- **同值按名称码点定序。** 服务端只保证按请求数降序，同值的相对次序来自 map 迭代；
  不额外钉一个次序，相同数据下表格顺序会跳。用码点而不是 `localeCompare`，免得结果
  随环境 locale 变、测试跟着飘

无障碍：当前排序列在 `th` 上写 `aria-sort`，未选中列**显式**写 `none`（省略与显式
none 在读屏器上表现不一致），箭头 `aria-hidden`，按钮可读名即列名。成本列带上
`kpi.currency`——`formatCost` 漏传币种会显示成裸数字。

**⑥ HTTP 数据源：填了等于没填的三处**（`77e235d`）

- **query 参数前端存得下、后端不读**：`toHTTPConfig` 不拷贝，`RunHTTP` 也没有这个
  字段，界面上看不出任何差别。现在与内置适配器共用 `joinURL`，两种写法（对象 / 字符串）、
  空值跳过、url 已有 `?` 时接 `&` 的判定两边一致。查询值**不做**占位符插值——内置
  适配器也不做，这份刻意的差别写进了测试
- **请求头原是 JSON 文本框**，少一个逗号整份被静默丢掉（build 判不出对象就不写）。
  改成与 constants / map / env 同一套"每行一条 `KEY=VALUE`"
- **basic 鉴权的用户名没有输入框**，后端只能拼出"空用户名:口令"，界面上完全看不出
  少了东西。补上用户名

顺带把 `auth.token` 纳入脱敏：它允许写死字面量令牌，与 `apiKey` 同属凭据，此前会以
明文经 `GET /api/quota/config` 下发且不做掩码回填。

**⑦ 峰谷计费补上前端入口**（`8380ae6`）

后端的分时段定价 S1.5 就做完了（四个端点 + 默认值 + 节假日同步），但界面上一直没有
入口，等于配置只能手改配置文件。配置页加一张卡片（内含编辑对话框）：开关、时区、
工作日定义、时段（增删 / 上下移 / 星期与工作日限定 / 乘数）、按日期的节假日覆盖、
节假日同步，以及"预览未来 N 天"。四处刻意的取舍：

- **预览不在前端重算**。哪一段命中、乘数是几，全部由 `POST /peak-pricing/preview`
  下发；从 periods 再推一遍等于把"首个命中者胜出""跨零点""按日期覆盖工作日"整套判定
  抄第二遍，抄错的那一天没人看得出来
- **不自动排序时段，也不把重叠当错误**。`ResolvePeriod` 的语义是首个命中者胜出，数组
  顺序就是优先级；排序会静默改掉"先特例后一般"的意图。重叠后端本就允许，界面只提示
  "这两段会同时命中、靠顺序裁决"，不拦保存——拦了等于前端凭空加一条后端没有的规则
- **时刻用文本框**而不是 `<input type="time">`：原生控件上限 23:59，表达不了后端认的
  `"24:00"`（写"一整天"的唯一写法）。配 `parseClock` 做镜像校验补回原生校验
- **卡片自带取数**，不并进配置页的 `Promise.all`：否则峰谷端点读失败会把整页说成
  "读取现有配置失败"，而同一页另外两张卡其实是好的

**真机撞出来的缺陷：同步节假日后抹掉还没保存的编辑**（`4fbd719`）

在 Chrome 里走"打开编辑器 → 把「启用峰谷计费」打开 → 点从上游同步 → 保存"，存进去的
`enabled` 是 `false`。界面上开关确实开着（保存前没人动它），只是没人再核对一眼。

根因是回填 effect 的依赖：`config` 会在两处换掉——保存成功，以及同步成功后 `doSync`
调 `onConfigChange` 把**服务端那一份**回传给父卡片（卡片要立刻显示"最近同步于…"）。
后一种情况带着的是旧的服务端版本，里面没有用户手上还没保存的开关与时段改动，于是依赖
一变，表单被整份重置回服务端版本，而且一声不响。`doSync` 本身只合并该进表单的三项
（覆盖表 / 同步时间 / 来源），设计是对的；错的是这条通路把整份配置都当成了"重新开草稿"
的理由。改法是依赖只留 `open`：对话框的语义是"打开即一份新草稿，打开期间外部不覆盖"。

**前后端功能对照表**（`3911282`）

用户要一份"整站"的对照表，落在 `docs/api-frontend-parity.md`（181 行）：管理侧 `/api`
端点、三个协议入口、非 HTTP 的触发通路（定时清理、外网同步等）、以及前端页面各自
依赖哪些后端能力。整理时顺手删掉 `api.ts` 里两个指向**不存在端点**的死包装——这张表
的第一个用处就是发现这种"看着接通、其实没有对方"。

**一处 a11y 收尾**（`14f8086`）：真机全流程走下来控制台唯一的一条噪音是「显示 Key」
对话框缺描述声明，同文件的编辑对话框早就按同一写法压掉了，这一处漏了。全站扫过，
缺这个声明的只有这一处。

**这一轮的真机验证**（另建二进制、独立临时目录、自造 token 与假上游，未碰仓库那份产物）

- 假上游（`mock_upstream.py`）扮演三件事：OpenAI 兼容的 chat 上游（含流式、可指定让
  某条必失败、两个模型耗时故意差开）、模型清单、以及配额页用的余量接口
- 真机跑出来的第一条硬结论是**假上游自己的编码问题**：Windows 控制台里 curl 带中文时
  body 常是 GBK，假上游直接 `json.loads(raw)` 抛 `UnicodeDecodeError`，处理线程带着连接
  一起崩——代理端只看到 EOF，于是报出一个跟真实原因毫无关系的错
  （`balancer pop err: no provide items or all items are disabled`），排查方向被带偏了
  一整轮。这也是 llmio 重试循环的一个性质：**首次失败的原因只在 `chat_logs.error` 里**，
  后一次 `Pop()` 失败会把前一次的错盖掉
- 峰谷计费按端到端核对：03:31 发一条请求 → 日志里 `peak_period` = 夜间优惠、
  `input_price` / `output_price` = 0.25 / 0.5（基础价 1 / 2 的 ×0.25），与默认时段的
  00:30–08:30 夜间优惠吻合
- 多选筛选、模型性能九列、环形/进度条各自指定、HTTP 源的多查询参数 + 两个自定义头 +
  Basic 用户名（假上游回吐 `auth=Basic cXVvdGEtdXNlcjpxdW90YXN…`，解开是
  `quota-user:quota-pass`）均在 Chrome 里逐项点过
- 请求对比页 `/compare` 需要带 `ids=` 进：真机上先建了一个开了 `io_log` 的密钥
  （IO 记录是**按密钥**开关的，最高权限 token 反而不记），发三条前缀相同的请求造出
  分叉，再核对公共前缀 1 项、逐条分叉点、以及缺 IO / id 不存在两种失败各自的原文

**测试与门禁**：26 个测试文件 / 579 例通过 + 1 条 `todo`（上一轮结束时是 21 / 456）；
A 层覆盖率门禁仍为 100%（语句 / 分支 / 函数 / 行）。本轮每一条新钉子都做了证伪：撤掉
源码里的对应改动确认对应用例转红，再还原。峰值计费的前端 A 层（`lib/peak.ts`）与
`lib/logs.ts` 一并纳入门禁。

**未做**：`log-chat` / `login` / `layout` / `quickstart` 四页仍无 C 层四态测试，
`logs` 的静默刷新分支仍未钉——与上一轮结束时相同，不是本轮新引入的。

**这一轮的 tag**：`v0.8.19` 打在本分支 HEAD 上（上一轮记的"出下一个版本时要重新决定
打在哪"，决定就是打在分支当前的最后一个提交上）。`v0.8.17` / `v0.8.18` 落在
`e1770fb` / `afd6c51`，在 `feat/console-redesign` 上；`feat/console-iter2` 从 `afd6c51`
接出来，因此三个 tag 在一条线上。`master` 仍是上游基线，没有合并。

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

**升级路径已验证**（`models/migrate_test.go`）

旧库不需要任何手工步骤：启动时的 `AutoMigrate` 就地升级，补列、建索引，紧接着那串兼容更新把
老行里的空值补齐。本方案对表结构的改动只有一处——`ChatLog` 展开内嵌的 `gorm.Model`、新增
`peak_period`、加 4 个索引（含 3 个复合）；其余 7 张表与上游完全一致。

这一条单独测，是因为新库上 `AutoMigrate` 总能建对，"新库能跑"证明不了升级路径可用。测试里的
旧库**不是手写的**：用上游代码自己建一个空库，把 `sqlite_master` 的语句导出成
`models/testdata/upstream_schema.sql`，再塞一批含空值、软删、用户已填值的行。断言覆盖每张表
的行数与关键字段不变、`peak_period` 补出来但不对历史行编造时段、上游那批单列索引仍在且新增复合
索引的**列与顺序**正确、兼容更新只补空值不覆盖用户填过的值、软删行既没被硬删也没被复活、旧行
落在新的 `created_at` 范围查询里、二次启动不改写任何一行。上面每一条都做过反证（改源码 → 对应
断言转红 → 还原）。

已知**未**回填：`chat_logs.currency`——上游的兼容更新只覆盖关联表。历史日志里的币种保持为空，
与上游一致；若要改口径，连同迁移测试里那条断言一起改。

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

**请求内容 `/logs/:id/chat-io`**（已重做）
- 保留流式分片重组、`reasoning_content`、`tool_calls` 参数分片合并、tool 结果反查
- **更正（2026-09-29）**：本节原先写「原实现是正则拼 HTML 并 `v-html` 注入，存在 XSS 面」——**这条描述是错的**。`v-html` 属于 dashboard 的 Vue 实现，被误套到了 React 版上。React 版用 `react-syntax-highlighter`，经查其实现**不含 `innerHTML`**，因此没有该 XSS 面。真实的改进点是体积：为给 JSON 染色，该库把约 627KB 的样式表打进了日志详情页的加载路径。已改为自绘的 `JsonTree`（零依赖、可折叠、类型双通道编码），该 chunk 不再随本页加载。
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
- `llmio_dashboard` 已存档 —— **未完成**：该目录不是 git 仓库，无可打的标签也无可提交的
  历史，此动作留给用户（见「S6 交付内容与偏差」的偏差一节）
