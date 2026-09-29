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
        "src/lib/theme.ts",
        "src/lib/utils.ts",
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
