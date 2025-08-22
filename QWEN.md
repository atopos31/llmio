# LLMIO 项目上下文总结

## 项目概述
LLMIO 是一个基于 Go 的现代化 LLM API 网关服务，提供统一的 API 接口来与多种大语言模型提供商进行交互。项目采用 Gin 框架构建后端，React + TypeScript + Vite 构建前端管理界面。

## 技术栈
- **后端**: Go 1.25+, Gin 框架, SQLite 数据库, GORM ORM
- **前端**: React 19, TypeScript, Vite, Tailwind CSS, Radix UI 组件库
- **部署**: Docker, Docker Compose
- **API 兼容**: OpenAI API 格式兼容

## 核心功能
1. **统一 API 网关**: 支持多种 LLM 提供商（目前支持 OpenAI）
2. **智能负载均衡**: 基于权重、成功率、响应时间的路由算法
3. **流式响应**: 支持流式和非流式两种响应模式
4. **提供商管理**: 可动态添加、配置和管理不同 LLM 提供商
5. **模型管理**: 管理可用模型及其与提供商的关联
6. **实时监控**: 请求统计、使用情况跟踪和日志查看
7. **Web 管理界面**: 现代化的响应式管理界面

## 项目结构
```
llmio/
├── main.go                 # 应用入口，路由配置
├── handler/               # HTTP 处理器
├── service/               # 业务逻辑（聊天补全服务）
├── providers/             # LLM 提供商实现（OpenAI 等）
├── balancer/              # 负载均衡算法
├── models/                # 数据库模型和初始化
├── middleware/            # 中间件（认证等）
├── common/                # 通用工具
├── webui/                 # 前端管理界面
├── db/                    # 数据库文件
├── Dockerfile & docker-compose.yml
└── go.mod & makefile
```

## API 端点
- `POST /v1/chat/completions` - OpenAI 兼容的聊天补全
- `GET /v1/models` - 获取可用模型列表
- `/api/*` - 管理 API（需要认证）

## Web UI 设计原则
- 保持组件默认的 dark/light 效果，不要自行定义 dark 模式
- 交互要美观流畅，注重用户体验
- 响应式设计，适配不同屏幕尺寸
- 使用现代 UI 组件库（Radix UI + Tailwind CSS）
- 简洁直观的管理界面

## 开发注意事项
1. **后端**: 遵循 Go 最佳实践，保持代码简洁高效
2. **前端**: 使用 TypeScript 确保类型安全，组件化开发
3. **API**: 保持与 OpenAI API 的兼容性
4. **数据库**: 使用 SQLite 存储配置和日志数据
5. **部署**: 支持 Docker 容器化部署

## 当前状态
项目已具备完整功能，包括：
- 基础的 OpenAI 提供商支持
- 负载均衡和请求路由
- Web 管理界面框架
- 数据库模型和初始化
- Docker 部署配置

## 扩展方向
1. 支持更多 LLM 提供商（Azure OpenAI, Anthropic, Cohere 等）
2. 增强负载均衡算法
3. 完善监控和告警功能
4. 优化前端用户体验
5. 添加更多管理功能

---