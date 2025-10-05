# ChatGPT 微服务 Demo（Go + gRPC + Gin）

> 一条命令拉起：HTTP 网关（Gin） → gRPC 微服务（Filter → Token → LLM/OpenAI → History），并带前端页面、限流、配额对齐与常见问题排查。

---

## 目录

* [项目概览](#项目概览)
* [快速开始（最少步骤）](#快速开始最少步骤)
* [依赖与环境变量](#依赖与环境变量)
* [启动脚本用法](#启动脚本用法)
* [服务与端口](#服务与端口)
* [HTTP API](#http-api)
* [前端页面](#前端页面)
* [数据层（MySQL + Redis）](#数据层mysql--redis)
* [配额与费用（真实 token 对齐）](#配额与费用真实-token-对齐)
* [常见问题排查](#常见问题排查)
* [Roadmap](#roadmap)

---

## 项目概览

* **语言/框架**：Go + gRPC + Gin
* **网关治理**：分段超时（Filter/Token 短、LLM 长）、错误分级（402/429/500）、本地令牌桶限流（默认 3 RPM）
* **LLM**：`llmserver` 直连 OpenAI（Chat Completions），可通过 `OPENAI_MODEL` 切换模型
* **配额**：`tokenserver`（Redis 版）支持**预占 + 真实用量对齐**，允许负数回冲，按日 TTL 重置
* **历史**：`historyserver` 持久化到 MySQL，并用 Redis 缓存**最近 N 条**
* **前端**：极简 SPA（`web/index.html`），与网关同端口服务
* **IDL**：`proto/chat.proto`（生成到 `chatpb/`，**勿手改**）

项目结构：

```
chatgpt-demo/
├─ proto/chat.proto          # protobuf 接口契约
├─ chatpb/                   # protoc 生成的 Go 代码（自动生成）
├─ tokenserver/              # Token（Redis 计数）
├─ historyserver/            # 历史（MySQL + Redis 缓存）
├─ filterserver/             # 文本过滤/清洗
├─ llmserver/                # LLM（OpenAI 接入）
├─ gateway/                  # HTTP 网关（Gin）
├─ web/index.html            # 前端页面（同端口服务）
└─ scripts/dev.sh            # 一键启动/停止/看日志
```

---

## 快速开始（最少步骤）

1. **准备外部依赖**（二选一）：

   * 本机已装 **Redis**（默认 `localhost:6379`）与 **MySQL 8**（默认 `root:root@localhost:3306`）。
   * 或自行用 Docker 起 Redis/MySQL（非必需）。
2. **设置 OpenAI Key**：

   ```bash
   export OPENAI_API_KEY="你的key"
   export OPENAI_MODEL="gpt-4o-mini"   # 可选
   ```
3. **一条命令拉起全部服务**：

   ```bash
   scripts/dev.sh up
   ```
4. 打开前端：`http://localhost:8080/`

> 初次运行会自动执行 `protoc` 生成代码。若未安装 `protoc`，脚本会跳过生成（但你修改了 proto 后需要安装并重新生成）。

---

## 依赖与环境变量

* **Go**：≥ 1.20（建议 1.22）
* **protoc**：用于从 `.proto` 生成 Go 代码
* **Redis**：默认 `localhost:6379`
* **MySQL**：默认 `root:root@localhost:3306`，数据库 `chatdb`

环境变量（可在 `~/.zshrc` 里长期配置）：

```bash
# OpenAI
export OPENAI_API_KEY=sk-xxxx
export OPENAI_MODEL=gpt-4o-mini

# Redis / MySQL（按你的环境调整）
export REDIS_ADDR=localhost:6379
export MYSQL_DSN='root:root@tcp(localhost:3306)/chatdb?parseTime=true&charset=utf8mb4,utf8'
```

---

## 启动脚本用法

脚本位于 `scripts/dev.sh`，后台运行所有服务并把日志写到 `logs/`。

```bash
# 启动全部服务（默认命令）
scripts/dev.sh up

# 停止全部服务
scripts/dev.sh down

# 重启全部服务
scripts/dev.sh restart

# 查看状态
scripts/dev.sh status

# 查看日志（单个/全部）
scripts/dev.sh logs gateway
scripts/dev.sh logs            # tail -F 所有日志

# （可选）用 Docker 起依赖
scripts/dev.sh deps up   # 无 Docker 会自动提示并跳过
scripts/dev.sh deps down
```

> 默认限流：网关单进程 **3 RPM**（与 Free 账户限速对齐）。

---

## 服务与端口

| 服务              |    端口 | 说明                           |
| --------------- | ----: | ---------------------------- |
| `gateway`       |  8080 | HTTP 网关（前端同端口）               |
| `tokenserver`   | 50051 | 配额（Redis 计数，按日 TTL，允许负数回冲）   |
| `filterserver`  | 50052 | 文本过滤/清洗                      |
| `historyserver` | 50054 | 历史持久化（MySQL）+ 最近缓存（Redis）    |
| `llmserver`     | 50055 | LLM（OpenAI Chat Completions） |

---

## HTTP API

### `POST /chat`

请求体：

```json
{ "user_id": "u1", "text": "Hello   world   from   Go!" }
```

成功响应（示例）：

```json
{
  "cleaned": "Hello world from Go!",
  "reply": "...",
  "usage": {"prompt_tokens": 12, "completion_tokens": 25, "total_tokens": 37},
  "remaining": 4963
}
```

错误响应（示例）：

* `400`：`{"error":"bad json or missing user_id"}` / `{"error":"text blocked by filter"}`
* `402`：`{"error":"insufficient_quota"}`（OpenAI 项目无额度）
* `429`：`{"error":"rate_limited"}`（速率限制；指数回退后重试）
* `500`：`{"error":"llm failed","detail":"..."}` / `token failed` / `filter failed`

### `GET /history?user_id=u1`

返回最近的消息（倒序写入，接口按时间顺序返回）。

```json
[
  {"role":"user","text":"..."},
  {"role":"assistant","text":"..."}
]
```

### `GET /health`

返回 `ok`。

---

## 前端页面

* 位置：`web/index.html`（由网关直接服务）。
* 访问：`http://localhost:8080/`
* 功能：输入 `user_id` 与 `text`，发送到 `/chat`，展示回复与用量；自动拉取 `/history`。

> 路由规则：未命中后端的请求通过 `NoRoute` 回退到 `index.html`，便于 SPA。

---

## 数据层（MySQL + Redis)

### MySQL（历史持久化）

初始化 SQL：

```sql
CREATE DATABASE IF NOT EXISTS chatdb;
USE chatdb;
CREATE TABLE IF NOT EXISTS chat_history (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id VARCHAR(64) NOT NULL,
  role ENUM('user','assistant') NOT NULL,
  text TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;
```

`historyserver` 读取 `MYSQL_DSN` 连接 MySQL；`List` 命中 Redis 直接返回，Miss 时查表（最近 N 条）。

### Redis（配额与缓存）

* 配额 Key：`token:{user}:{yyyy-mm-dd}`，使用 `INCRBY`；首次设置 `EXPIRE`（默认 48h）。
* 允许**负数回冲**（用于把“预占 200”对齐到真实 token 用量）。
* 最近对话缓存：`history:{user}` 使用 `LPUSH + LTRIM`，默认缓存最近 40 条。

---

## 配额与费用（真实 token 对齐）

* 网关在调用 LLM 前先**预占** `200` 个 token，确保超配额能提前拦截。
* LLM 返回 `usage.total_tokens` 后，网关计算 `delta = total - 200`，再次调用 Token：

  * `delta > 0`：**补扣**
  * `delta < 0`：**回冲**（负数），服务端下限保护到 0
* 好处：避免固定扣减带来的“高估/低估”，成本与配额实时一致。

> 免费层一般有 **3 RPM** 限速与配额门槛；充值/升级后问题即可缓解。我们在网关内置了 3 RPM 令牌桶，防止误触上限。

---

## 常见问题排查

* **前端访问不到**：确认 `web/index.html` 路径正确；`curl -I http://localhost:8080/` 是否 `200 OK`。
* **`llm failed` + `insufficient_quota`**：OpenAI 项目无额度；前往 Billing 充值或选择正确 Project。
* **429 `rate_limited`**：触发速率限制；降低并发/频率或等更高 usage tier。
* **`protoc-gen-go: program not found`**：安装插件并把 `$(go env GOPATH)/bin` 加入 PATH。
* **MySQL 连接失败**：检查 `MYSQL_DSN`；root 无密码时去掉 `:root@`；确认 `chatdb` 与表已创建。
* **Redis 连接失败**：确认 `REDIS_ADDR`，`redis-cli ping` 预期 `PONG`。
* **端口占用**：修改服务端口或释放端口（macOS：`lsof -i :8080`）。

---

## Roadmap

1. **可观测性**：Prometheus 指标（QPS/延迟/错误码）、结构化日志（zap）、trace_id 透传
2. **SSE/WebSocket 流式**：`/chat/stream`，边生成边推送
3. **Docker Compose**：一键容器化 Redis/MySQL/五个服务
4. **KeywordService**：关键词抽取/检索增强示例
5. **分布式限流**：基于 Redis 的全局令牌桶（多实例共享）

---

## 变更日志（关键里程碑）

* v0.4：加 History（MySQL+Redis 缓存）；网关保存历史；前端页面上线
* v0.3：Token 切 Redis，支持预占 + 真实用量对齐、负数回冲
* v0.2：接入 OpenAI；网关分段超时 + 错误分级 + 本地 3 RPM 限流
* v0.1：最小链路（Filter → LLM Stub）+ Gin 网关 + 客户端验证
