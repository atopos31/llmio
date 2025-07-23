# LLMIO

LLMIO 是一个基于 Go 的服务，提供统一的 API 来与各种大语言模型（LLM）进行交互。它支持在不同提供商之间进行负载均衡，并高效地处理请求。

## 功能特性

- 统一 API 访问多种 LLM 提供商
- 带有权重随机选择的负载均衡
- 支持流式和非流式响应
- 处理速率限制
- 使用情况跟踪和日志记录

## 快速开始

### 先决条件

- Go 1.22+
- SQLite（或 GORM 支持的其他数据库）

### 安装

1. 克隆仓库：
   ```bash
   git clone https://github.com/atopos31/llmio.git
   cd llmio
   ```

2. 安装依赖：
   ```bash
   go mod tidy
   ```

3. 初始化数据库：
   ```bash
   go run main.go
   ```
   这将自动创建一个 SQLite 数据库文件（`llmio.db`）并初始化数据库结构。

### 配置

该服务使用数据库来存储提供商和模型的配置。你需要通过直接操作数据库或构建管理界面来添加提供商和模型。

OpenAI 提供商配置示例：
- 名称: openai
- 类型: openai
- 配置: `{"base_url": "https://api.openai.com/v1", "api_key": "your-api-key"}`

模型配置示例：
- 名称: gpt-3.5-turbo
- 备注: OpenAI 的 GPT-3.5 Turbo 模型

### 运行服务

启动服务：
```bash
go run main.go
```

服务将在 `http://localhost:7070` 可用。

## API 端点

### 聊天补全

POST `/v1/chat/completions`

请求体遵循 OpenAI 聊天补全 API 格式。

示例：
```json
{
  "model": "gpt-3.5-turbo",
  "messages": [
    {
      "role": "user",
      "content": "Hello!"
    }
  ],
  "stream": true
}
```

### 模型列表

GET `/v1/models`

返回可用模型的列表。

## 架构

该服务由以下组件构成：

- **main.go**: 应用程序入口点
- **handler/**: API 端点的 HTTP 处理器
- **service/**: 聊天补全和负载均衡的业务逻辑
- **providers/**: 不同 LLM 提供商的实现
- **models/**: 数据库模型和初始化
- **balancer/**: 负载均衡算法
- **common/**: 通用工具和响应助手

## 贡献

欢迎贡献！请提交 issue 或 pull request。

## 许可证

该项目基于 MIT 许可证。