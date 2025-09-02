# LLMIO 项目规范与标准

## 项目概览

LLMIO 是一个基于 Go 的 LLM 代理服务，提供统一的 API 接口来访问多个大语言模型提供商，支持智能负载均衡和现代化的 Web 管理界面。

## 架构规范

### 后端架构
- **语言**: Go 1.25.0+
- **框架**: Gin Web Framework
- **数据库**: SQLite (通过 GORM ORM)
- **架构模式**: 分层架构 (Handler → Service → Provider)
- **并发模型**: Go routines + Channels

### 前端架构
- **语言**: TypeScript
- **框架**: React 19
- **构建工具**: Vite
- **路由**: React Router DOM 7
- **样式**: Tailwind CSS 4 + Radix UI
- **状态管理**: React Hooks + Context
- **表单**: React Hook Form + Zod

## 代码规范

### Go 编码规范

#### 1. 代码格式
- 使用 `gofmt` 格式化所有 Go 代码
- 行长度限制：120 字符
- 使用制表符缩进

#### 2. 命名规范
- **包名**: 全小写，短且语义明确 (`handlers`, `models`, `providers`)
- **结构体**: 驼峰命名，大写开头 (`ChatRequest`, `ProviderConfig`)
- **接口名**: 以 `-er` 结尾 (`ProviderProvider`, `Balancer`)
- **函数名**: 动词+名词，描述清晰 (`getProviderByID`, `validateAPIKey`)
- **变量名**: 驼峰命名，避免简写 (`requestData`, `providerList`)
- **常量名**: 驼峰或全大写+下划线 (`MaxRetries`, `DEFAULT_TIMEOUT`)

#### 3. 代码结构
```
├── handler/           # HTTP 处理器
│   ├── api.go        # 主API路由处理
│   ├── chat.go       # 聊天相关功能
│   ├── home.go       # 主页和静态资源
│   └── test.go       # 测试相关
├── service/          # 业务逻辑层
│   ├── chat.go       # 聊天核心业务逻辑
│   ├── balancer.go   # 负载均衡算法
│   └── middleware/   # 中间件
├── providers/        # LLM提供商实现
│   ├── provider.go   # 提供商接口定义
│   ├── openai.go    # OpenAI提供商实现
│   └── anthropic.go  # Anthropic提供商实现
├── models/          # 数据模型
│   ├── model.go     # 数据库模型定义
│   └── init.go      # 数据库初始化
└── common/          # 公共工具
    └── response.go  # 统一响应格式
```

#### 4. 错误处理
- 使用标准库 `errors` 包
- 自定义错误类型：`ErrProviderUnavailable`, `ErrInvalidRequest`
- 统一错误响应格式：
```go
type APIResponse struct {
    Success bool        `json:"success"`
    Data    interface{} `json:"data,omitempty"`
    Message string      `json:"message,omitempty"`
    Error   string      `json:"error,omitempty"`
}
```

#### 5. 日志规范
- 使用结构化日志 (JSON 格式)
- 日志级别：INFO, WARN, ERROR, DEBUG
- 关键操作必须包含请求ID、跟踪ID
- 格式：时间戳 | 级别 | 组件 | 消息 | 上下文

#### 6. 接口设计
- RESTful API 设计
- 统一前缀：/v1/
- JSON 响应格式
- HTTP 状态码语义化使用
- 分页响应格式：
```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "per_page": 10,
    "total": 100,
    "total_pages": 10
  }
}
```

### TypeScript/React 编码规范

#### 1. 代码格式
- 使用 ESLint + Prettier
- 行长度限制：100 字符
- 2个空格缩进
- 分号必须

#### 2. 命名规范
- **组件**: PascalCase (`ChatInterface`, `ModelCard`)
- **函数**: camelCase (`fetchData`, `handleSubmit`)
- **文件**: 与导出同名 (组件使用 PascalCase)
- **类型**: PascalCase + Type (`UserType`, `ApiResponseType`)
- **枚举**: PascalCase + Enum (`StatusEnum`, `MessageTypeEnum`)
- **常量**: SCREAMING_SNAKE_CASE
- **接口**: PascalCase + Props (`ChatProps`, `FormProps`)

#### 3. 组件规范
- 函数组件优先
- 使用 TypeScript 严格模式
- Props 类型定义必填
- 默认导出主组件
- 使用 React.FC 类型

#### 4. 状态管理
- React Context 用于全局状态
- useState/useReducer 用于局部状态
- SWR/React Query 用于数据获取
- Zustand 作为备用状态管理方案

#### 5. 样式规范
- Tailwind CSS 优先
- CSS Modules 用于复杂样式
- CSS-in-JS (通过 Tailwind) 可接受
- 避免直接使用内联样式
- 响应式优先设计 (移动优先)

#### 6. 文件结构
```
src/
├── components/       # 可复用组件
│   ├── ui/          # 基础UI组件 (Button, Input等)
│   ├── charts/      # 图表组件
│   └── forms/       # 表单组件
├── routes/          # 页面路由组件
│   ├── layout/      # 布局组件
│   ├── home/        # 主页
│   └── logs/        # 日志页面
├── lib/            # 工具函数
│   ├── api.ts       # API调用
│   └── utils.ts     # 通用工具
├── hooks/          # 自定义Hook
└── types/          # 类型定义
```

## 数据库规范

### Schema 设计
- 使用 GORM 自动迁移
- 复数字段名用于数组/切片
- 时间字段使用 `gorm:"autoCreateTime"`, `gorm:"autoUpdateTime"`
- 外键命名：`ModelID` → `model_id`

### 模型标准
```go
type Model struct {
    ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
    Name       string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"name"`
    Description string   `gorm:"type:text" json:"description"`
    CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
```

## API 设计规范

### 通用响应格式
```json
{
  "success": true,
  "data": {},
  "message": "处理成功",
  "error": null
}
```

### 错误响应格式
```json
{
  "success": false,
  "error": "VALIDATION_ERROR",
  "message": "请求参数验证失败"
}
```

### HTTP 状态码使用
- 200: 成功 (GET/PUT)
- 201: 创建成功 (POST)
- 204: 成功无内容 (DELETE)
- 400: 请求错误
- 401: 未授权
- 403: 权限不足
- 404: 资源不存在
- 422: 验证失败
- 500: 服务器错误

### 标准API端点
```
CRUD操作：
GET    /api/resources
POST   /api/resources
GET    /api/resources/:id
PUT    /api/resources/:id
DELETE /api/resources/:id

嵌套资源：
GET    /api/parents/:parentId/children
POST   /api/parents/:parentId/children/:id
```

## 安全规范

### 认证授权
- Token 基于 Bearer Token
- 使用 JWT 可选
- 敏感数据加密存储
- 环境变量存储密钥
- HTTPS 生产环境必须

### 输入验证
- 后端必须验证所有输入
- SQL 注入防护：使用 GORM 防注入
- XSS 防护：输出转义
- CSRF 防护：生产环境添加
- 速率限制：API限流

### 安全配置
- 数据库：避免明文存储 API 密钥
- 文件权限：敏感文件权限限制
- 日志：不记录敏感信息
- 错误：生产环境隐藏详细错误

## 开发工作流

### 开发环境
1. 前置要求：
   - Go 1.25.0+
   - Node.js 20+
   - pnpm (包管理器)
   - SQLite3

2. 安装步骤：
   ```bash
   # 后端依赖
   go mod tidy
   
   # 前端依赖
   cd webui && pnpm install
   
   # 数据库初始化
   go run main.go
   
   # 启动开发服务器
   # 后端: go run main.go
   # 前端: cd webui && pnpm dev
   ```

3. 调试配置：
   - `.env.local` 用于本地配置
   - GIN_MODE=debug 用于详细日志
   - DEBUG=true 用于 API 调试

### 测试规范
1. 单元测试：
   ```bash
   # 后端测试
   go test ./...
   
   # 前端测试  
   cd webui && pnpm test
   ```

2. 集成测试：
   - API 测试：Postman 集合
   - 前端测试：Playwright

3. 代码质量：
   - 静态检查：`golangci-lint`
   - 安全扫描：govulncheck
   - 依赖更新：定期安全更新

### Git 工作流
1. 分支策略：
   - main: 生产分支
   - develop: 开发分支
   - feature/*: 功能分支
   - hotfix/*: 热修复分支

2. 提交规范：
   ```
   [类型]: [简要描述]
   
   [详细描述]
   
   [相关 issue #123]
   
   类型: feat, fix, docs, style, refactor, test, chore
   ```

3. 代码审查：
   - Pull Request 必须评审
   - 所有测试必须通过
   - 代码覆盖率 >80%

### 部署规范
1. 构建流程：
   ```bash
   # 检查代码
   golangci-lint run
   go test ./...
   
   # 构建前端
   cd webui && pnpm build
   
   # 构建后端
   go build -o llmio
   ```

2. Docker 部署：
   - 使用多阶段构建
   - 运行时镜像使用 alpine:latest
   - 非 root 用户运行
   - 最小化镜像大小

3. 环境配置：
   - 开发: `GIN_MODE=debug`
   - 测试: `GIN_MODE=test`
   - 生产: `GIN_MODE=release`

## 性能规范

### 后端优化
1. 数据库：
   - 合理索引设计
   - 查询优化
   - 连接池管理

2. API：
   - 响应时间 <500ms
   - 并发限流
   - 缓存策略

3. 内存：
   - 避免内存泄露
   - 及时释放资源
   - 合理使用缓冲

### 前端优化
1. 加载性能：
   - 代码分割
   - 懒加载路由
   - 图片优化
   - 缓存策略

2. 运行性能：
   - 虚拟滚动
   - 防抖/节流
   - 避免重渲染
   - 内存管理

3. 体验优化：
   - 骨架屏
   - 渐进式加载
   - 错误边界
   - 离线支持

## 监控与日志

### 后端监控
1. 应用监控：
   - 响应时间
   - 错误率
   - 吞吐量
   - 服务健康

2. 业务监控：
   - API 调用统计
   - 提供商使用情况
   - 模型分布
   - 用户行为

3. 日志规范：
   - 结构化日志 (JSON)
   - 集中式收集
   - 日志轮转
   - 错误聚合

### 前端监控
1. 性能监控：
   - 页面加载时间
   - API 响应时间
   - 资源加载失败
   - 用户体验指标

2. 业务监控：
   - 用户行为追踪
   - 功能使用情况
   - 错误统计
   - 性能瓶颈

## 故障处理

### 错误处理原则
1. 用户友好的错误信息
2. 详细的日志记录
3. 优雅降级
4. 快速恢复机制

### 常见场景处理
1. 数据库连接失败：
   - 重试机制
   - 连接池管理
   - 降级模式

2. 外部API超时：
   - 超时配置
   - 重试策略  
   - 备选方案

3. 资源耗尽：
   - 内存限制
   - 连接数限制
   - 队列管理

---

## 更新记录

- 版本：1.0.0
- 日期：2025-09-02
- 说明：项目初始规范文档

---

**注意**: 此规范文档会随项目发展持续更新，请定期检查最新版本。