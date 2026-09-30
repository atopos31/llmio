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
