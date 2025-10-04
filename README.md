# ChatGPT 微服务 Demo（Go + gRPC + Gin）

> 本 README 记录我们从零搭建“HTTP 网关 → gRPC 微服务（Filter → Token → LLM Stub）”的全部步骤、测试方法与常见坑，便于复现与口述面试亮点。

---

## 一、项目概览

* **语言/框架**：Go + gRPC + Gin
* **服务组成**：

  * `gateway`：HTTP 入口（Gin），编排调用 gRPC：Filter → Token → LLM
  * `filterserver`：文本过滤与清洗
  * `tokenserver`：配额检查（内存版；后续可换 Redis）
  * `llmserver`：LLM Stub（回声回复，用于先跑通链路）
* **IDL**：`proto/chat.proto`（定义消息与服务接口）
* **生成代码目录**：`chatpb/`（由 `protoc` 生成，**勿手改**）

端口约定：

* Token：`:50051`
* Filter：`:50052`
* LLM：`:50055`
* Gateway：`:8080`

---

## 二、目录结构

```
chatgpt-demo/
├─ proto/chat.proto         # protobuf 接口契约（LLM/Filter/Token）
├─ chatpb/                  # protoc 生成的 gRPC/Proto Go 代码（自动生成）
├─ llmserver/main.go        # LLM Stub 服务（Echo）
├─ filterserver/main.go     # 文本过滤服务
├─ tokenserver/main.go      # 配额服务（内存实现）
└─ gateway/main.go          # HTTP 网关（Gin）→ gRPC 串联
```

---

## 三、环境与依赖

* Go ≥ 1.20（建议 1.22）
* `protoc`（Protocol Buffers 编译器）
* 代码生成插件：

  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  ```
* 确保 `$(go env GOPATH)/bin` 在 PATH：

  ```bash
  echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc
  source ~/.zshrc
  ```

生成代码：

```bash
protoc -I=proto --go_out=. --go-grpc_out=. proto/chat.proto
```

> 如遇 PATH 问题，可临时指定插件：
>
> ```bash
> protoc -I=proto \
>   --plugin=protoc-gen-go=$(go env GOPATH)/bin/protoc-gen-go \
>   --plugin=protoc-gen-go-grpc=$(go env GOPATH)/bin/protoc-gen-go-grpc \
>   --go_out=. --go-grpc_out=. \
>   proto/chat.proto
> ```

依赖拉取：

```bash
go get google.golang.org/grpc@latest \
       google.golang.org/grpc/credentials/insecure@latest \
       github.com/gin-gonic/gin@latest

go mod tidy
```

---

## 四、逐步里程碑（从零到完整链路）

### 里程碑 0：环境自检

```bash
go version
protoc --version
```

### 里程碑 1：定义最小 LLMService（Echo）

* `proto/chat.proto` 定义 `LLMService.Generate(ChatRequest) -> ChatResponse`
* 运行 `protoc ...` 生成 `chatpb/`
* 实现 `llmserver/main.go`：注册并监听 `:50055`
* 写 `client/main.go`：用 `insecure.NewCredentials()` 拨号并调用

**测试**：

```bash
# 终端A
go run ./llmserver
# 终端B
go run ./client "这是一条最小链路测试"
# 期望
LLM reply => Echo: 这是一条最小链路测试
```

### 里程碑 2：加入 FilterService（违禁词与清洗）

* 扩展 `proto/chat.proto`：添加 `FilterService.Filter`
* 生成代码 → 实现 `filterserver/main.go`（监听 `:50052`）
* 客户端先调 Filter 再调 LLM

**测试**：

```bash
# 终端A
go run ./filterserver
# 终端B
go run ./llmserver
# 终端C
go run ./client "Hello   world   from   Go!"    # 清洗成单空格
# 期望
✅ 过滤后文本： Hello world from Go!
LLM reply => Echo: Hello world from Go!

# 含违禁词
go run ./client "this has foo inside"
# 期望：
❌ 文本被过滤（包含违禁词）： this has foo inside
```

### 里程碑 3：加 HTTP 网关（Gin）

* 新建 `gateway/main.go`：

  * 启动时各拨一个 gRPC 长连接：Filter/LLM
  * 提供 `POST /chat`：`{user_id, text}`
  * 顺序：Filter → LLM → 返回 JSON

**测试**：

```bash
# 终端A
go run ./filterserver
# 终端B
go run ./llmserver
# 终端C
go run ./gateway

# 正常文本
curl -s -X POST http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"u1","text":"Hello   world   from   Go!"}'
# 期望
{"cleaned":"Hello world from Go!","reply":"Echo: Hello world from Go!"}

# 违禁词
curl -s -X POST http://localhost:8080/chat \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"u1","text":"this has foo inside"}'
# 期望
{"error":"text blocked by filter"}
```

### 里程碑 4：加入 TokenService（配额内存版）

* 扩展 `proto/chat.proto`：`TokenService.CheckAndInc`
* 生成代码 → 实现 `tokenserver/main.go`（监听 `:50051`）
* 网关在 Filter 后、LLM 前调用 Token：每次假定消耗 50 tokens

**测试**：

```bash
# 终端A/B/C/D 分别运行四个服务
go run ./filterserver
go run ./tokenserver
go run ./llmserver
go run ./gateway

# 连续 5 次请求（limit=200，每次50，第5次将超限）
for i in {1..5}; do
  curl -s -X POST http://localhost:8080/chat \
    -H 'Content-Type: application/json' \
    -d '{"user_id":"u1","text":"quick call"}'; echo; done

# 期望：前四次 200，附带 remaining 值递减；最后一次 429：
{"error":"quota exceeded","remaining":-50}
```

---

## 五、HTTP API 说明（当前网关）

### `POST /chat`

**Request JSON**

```json
{
  "user_id": "u1",
  "text": "Hello   world   from   Go!"
}
```

**Responses**

* `200 OK`

  ```json
  { "cleaned": "Hello world from Go!", "reply": "Echo: Hello world from Go!", "remaining": 150 }
  ```
* `400 Bad Request`

  ```json
  { "error": "bad json or missing user_id" }
  ```

  或

  ```json
  { "error": "text blocked by filter" }
  ```
* `429 Too Many Requests`

  ```json
  { "error": "quota exceeded", "remaining": -50 }
  ```
* `500 Internal Server Error`

  ```json
  { "error": "filter failed" }
  ```

  / `llm failed` / `token failed`

### `GET /health`

* 返回 `ok`（网关存活检查）

---

## 六、常见问题与排查

* **`protoc-gen-go: program not found`**：

  * 安装插件，并把 `$(go env GOPATH)/bin` 加入 PATH；或在 protoc 命令里显式 `--plugin=...`
* **`google.golang.org/grpc` 报错**：

  * 执行 `go get google.golang.org/grpc@latest && go mod tidy`
* **`WithInsecure` 被废弃**：

  * 使用 `credentials/insecure`：

    ```go
    grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    ```
* **端口被占用**：

  * 修改服务监听端口，或查 `lsof -i :PORT` 后释放
* **调用卡住**：

  * 确认在客户端/网关使用 `context.WithTimeout` 设置超时

---

## 七、面试可讲亮点（本 Demo 已具备）

* **IDL 优先**：以 proto 定义服务与消息，生成强类型客户端/服务端代码
* **服务编排与前置治理**：在 LLM 前做文本过滤与配额拦截，节省计算资源
* **超时与错误语义**：全链路 `context` 超时；HTTP 语义化状态码（400/429/500）
* **可扩展性**：LLM Stub 易于切换为真实模型；Token 可替换 Redis；History 可上 MySQL
* **可观测性挂载点**：在网关统一打日志、metrics、trace 最方便

---

## 八、下一步（Roadmap）

1. **TokenService → Redis**（计数 + TTL 按天重置；`go-redis`）
2. **HistoryService → MySQL**（保存/分页查询聊天历史）
3. **SSE/WebSocket 流式回复**（更贴近真实 Chat 体验）
4. **Prometheus + Grafana**（QPS、p95 延迟、命中率、错误码分布）
5. **统一日志/TraceID**（网关生成 trace_id，gRPC metadata 透传）
6. **容器化与编排**（Docker Compose → Kubernetes）

---

## 九、清理

结束开发后可 Ctrl + C 结束各进程；或统一脚本化管理多进程（后续可补 `Makefile`/`Taskfile`）。

---

## 十、License

自用学习示例，未附带 License。按需添加。
