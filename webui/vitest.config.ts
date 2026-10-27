import { fileURLToPath } from "node:url"

import react from "@vitejs/plugin-react-swc"
import { defineConfig } from "vitest/config"

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      /**
       * 只把纯逻辑层纳入覆盖率门禁（方案 §5 的 A 层）：
       * 无 IO、无渲染，每个分支都对应一个真实的语义决定。
       * 组件与页面按四态（加载/空/错误/有数据）覆盖，不设百分比——
       * 对展示型组件追 100% 会催生断言实现细节的脆性测试，是负收益。
       *
       * api.ts 是**已知的例外**，不在门禁内：它由 38 个同构的端点包装组成，
       * 逐条枚举测试的收益低于成本（URL 正确性最终由联调与页面集成测试保证）。
       * 其中真正有逻辑的 apiRequest 核心与 checkLatestRelease 已在
       * api.test.ts 中覆盖。补齐端点清单是后续可选工作，不是静默遗漏。
       */
      include: [
        "src/lib/palette.ts",
        // 结构化请求/响应内容的解析层（W3，四协议）。每个分支都是语义决定：
        // 协议形状嗅探、tool call 按 id 与 index 的双向累积、thinking 与正文分桶、
        // 坏 JSON 与未知块的降级计数。判错时页面照常渲染，读者看到的却是错的
        // ——所以必须 100%。fixtures.ts 是夹具数据、不含逻辑，不进门禁。
        "src/lib/chat-io/*.ts",
        // 展示层格式化。原本不在门禁内，加进来是因为它现在持有**语义决定**
        // 而不只是拼字符串：跨天的序列要不要在刻度上带日期，判断错了页面
        // 照样渲染，只是轴上的时刻重复出现、读者分不清哪段是哪天。
        "src/lib/format.ts",
        "src/lib/theme.ts",
        "src/lib/utils.ts",
        // 配额页的纯逻辑：格式引擎镜像、状态排序、展示偏好。
        // 归入 A 层的理由与 palette 相同——它每一个分支都对应一个语义决定
        // （未知占位符原样保留、未知状态不排到最后、清除覆盖而非置假值），
        // 漏测就是漏语义，而不是漏了一行展示代码。
        "src/lib/quota.ts",
        // 分析页的纯逻辑：时间范围口径、筛选到查询串的映射、
        // 错误类别的呈现规则。这些同样是语义承诺——预设窗口的边界算错、
        // 密钥筛选传名字而不是 id，页面照样渲染，只是数字的含义变了。
        "src/lib/analytics.ts",
        // 日志页的纯逻辑：多选筛选在 URL / 查询串之间的往返。加进来是因为
        // 它同样持有语义承诺——"没有选"与"选了一个叫 all 的值"要不要区分、
        // 逗号是不是多值的编码口径，判错了页面照常渲染，只是筛出来的东西不对。
        "src/lib/logs.ts",
        // 日志表列注册表：成本计算、缓存命中率、可见列过滤。每个分支都是语义
        // 决定——成本算错改的是每一笔请求的价格，缓存率算错改的是每一行的
        // 命中率显示，可见列过滤判错就变成"勾选了但列不出现"。
        "src/lib/logs-columns.ts",
        // 跨页面 UI 偏好（自动刷新档位、总览时间范围、日志页可见列）。
        // 进来是因为它守着一条例错的规则：旧版本存过的刷新档位若已不在
        // 当前选项里，必须回落"关闭"而不是照发请求——判错时页面照常渲染，
        // 只是界面上没有任何一个按钮对应那个档位，用户无从察觉矛盾。
        "src/lib/prefs.ts",
        // 峰谷计费的纯逻辑：时刻解析、跨零点与重叠判定、与后端 validatePeakPricing
        // 对齐的本地校验、表单↔载荷映射。进来是因为它每个分支都是语义决定——
        // "24:00 算不算合法""start==end 怎么算""重叠报不报错""乘数空的该发什么"，
        // 判错了页面照常渲染，改的是每一笔请求的价格。
        "src/lib/peak.ts",
        // 转发专页的纯逻辑：地址按协议拼接（Anthropic 不带 /v1、Gemini 带 /v1beta）、
        // 关闭模式下那 5 条可用端点、slug 规则、后端三态到弹窗三态的映射，以及
        // "首次引导已读"的 localStorage 往返。判错时页面照常渲染，只是给出的地址
        // 粘进客户端必然 404，属于典型的"错得看不出来"。
        "src/routes/forwarding/forwarding-utils.ts",
        "src/routes/forwarding/guide-storage.ts",
        "src/utils/**/*.ts",
        "src/hooks/**/*.ts",
        "src/stores/**/*.ts",
        "src/components/theme-provider.tsx",
      ],
      exclude: ["src/**/*.test.{ts,tsx}", "src/test/**"],
      thresholds: {
        statements: 100,
        branches: 100,
        functions: 100,
        lines: 100,
      },
    },
  },
})
