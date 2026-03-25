# Strait

**OpenAI 兼容的 LLM 网关。只做一件事，并做到极致。**

`Strait` 是一个用 Go 编写的高性能、窄边界代理。它允许你在保持使用 OpenAI SDK 的同时，将请求路由到多个供应商（OpenAI、Anthropic 等），并具备工业级的稳定性。

### 核心原则
- **OpenAI 兼容**：对任何 OpenAI SDK 客户端实现无缝替换。
- **协议转换**：原生支持 Anthropic (Claude) 等供应商的协议转换。
- **极致性能**：零分配的 SSE 转换，极低的处理开销。
- **内置稳定性**：具备熔断器、带退避的重试以及主动健康探测。
- **零脂肪**：无缓存、无数据库、无状态。纯粹的 Unix 风格代理。

---

### 🚀 一行命令启动

```bash
make run
```
*在 `http://localhost:8080` 启动网关。默认使用 `.env.example` 和 `config.example.yaml`。*

### 🚢 一行命令部署

```bash
make deploy
```
*在极简的 Docker 容器中构建并启动网关。*

---

### 使用方法

**聊天补全 (Chat Completions):**
```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_TOKEN" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

**健康状态:**
```bash
curl http://localhost:8080/health/ready
```

### 配置
所有配置都在 `config.yaml` 中。敏感密钥从 `.env` 中定义的环境变量加载。

```yaml
auth:
  tokens:
    - name: "my-app"
      value_env: "MY_APP_TOKEN"

providers:
  - name: "siliconflow"
    base_url: "https://api.siliconflow.cn"
    api_key_env: "SILICONFLOW_API_KEY"
    models: ["Pro/MiniMaxAI/MiniMax-M2.5"]

  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    api_key_env: "ANTHROPIC_API_KEY"
    models: ["claude-3-5-sonnet-latest"]
```

`base_url` 请填写 provider 主机根地址。像 SiliconFlow 这种 OpenAI-compatible 后端，网关会自己追加 `/v1/chat/completions`。

### 可观测性
- **日志**：通过标准库 `slog` 输出结构化 JSON 日志。
- **指标**：在 `/metrics` 提供 Prometheus 指标。
- **优雅停机**：具备 300 秒停机窗口，确保长耗时的流式请求平滑结束。

---
许可证: MIT
