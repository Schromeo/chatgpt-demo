package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	pb "chatgpt-demo/chatpb"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// 建立到 gRPC 服务的长连接（网关启动时创建一次）
func mustDial(addr string) *grpc.ClientConn {
	cc, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	return cc
}

func main() {
	// 连接各后端 gRPC 服务
	tokenConn := mustDial("localhost:50051")
	defer tokenConn.Close()
	filterConn := mustDial("localhost:50052")
	defer filterConn.Close()
	historyConn := mustDial("localhost:50054")
	defer historyConn.Close()
	llmConn := mustDial("localhost:50055")
	defer llmConn.Close()

	// gRPC 客户端
	tokenCli := pb.NewTokenServiceClient(tokenConn)
	filterCli := pb.NewFilterServiceClient(filterConn)
	historyCli := pb.NewHistoryServiceClient(historyConn)
	llmCli := pb.NewLLMServiceClient(llmConn)

	// Gin 路由
	r := gin.Default()

	// 静态前端（可选）：访问 http://localhost:8080/
	r.StaticFile("/", "./web/index.html")

	// 简单限流（与 Free 3 RPM 对齐；多实例需分布式限流）
	limiter := rate.NewLimiter(rate.Every(time.Minute/3), 3) // 3 次/分钟，突发 3

	// 请求体
	type chatReq struct {
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// 查询历史
	r.GET("/history", func(c *gin.Context) {
		user := c.Query("user_id")
		if user == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing user_id"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 1*time.Second)
		defer cancel()
		resp, err := historyCli.List(ctx, &pb.ListRequest{UserId: user, Limit: 20})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "history failed", "detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, resp.Items)
	})

	// 核心入口：HTTP → (Filter → Token 预占 → LLM → Token 对齐 → Save History)
	r.POST("/chat", func(c *gin.Context) {
		// 限流
		if err := limiter.Wait(c.Request.Context()); err != nil {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate_limited"})
			return
		}

		var req chatReq
		if err := c.BindJSON(&req); err != nil || req.UserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad json or missing user_id"})
			return
		}

		// 根上下文（绑定到本次 HTTP 请求）
		root := c.Request.Context()

		// 1) 文本过滤 / 清洗（本地 gRPC，800ms）
		fctx, fcancel := context.WithTimeout(root, 800*time.Millisecond)
		defer fcancel()

		fr, err := filterCli.Filter(fctx, &pb.FilterRequest{Text: req.Text})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter failed", "detail": err.Error()})
			return
		}
		if !fr.GetAllowed() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text blocked by filter"})
			return
		}

		// 2) 预占配额（本地 gRPC，800ms）
		tctx, tcancel := context.WithTimeout(root, 800*time.Millisecond)
		defer tcancel()

		const preReserve = int32(200) // 先预占 200 tokens，调用后用真实用量对齐
		tr1, err := tokenCli.CheckAndInc(tctx, &pb.TokenRequest{
			UserId: req.UserID, Tokens: preReserve,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token failed", "detail": err.Error()})
			return
		}
		if !tr1.GetAllowed() {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "quota exceeded", "remaining": tr1.GetRemaining()})
			return
		}

		// 3) 调用 LLM（外部服务，给 12s）
		lctx, lcancel := context.WithTimeout(root, 12*time.Second)
		defer lcancel()

		lr, err := llmCli.Generate(lctx, &pb.ChatRequest{
			UserId: req.UserID, Text: fr.GetCleaned(),
		})
		if err != nil {
			msg := err.Error()
			// 额度不足（需要充值或开通计费）
			if strings.Contains(msg, "insufficient_quota") {
				c.JSON(http.StatusPaymentRequired, gin.H{
					"error":  "insufficient_quota",
					"detail": "OpenAI 项目无可用额度：请在 Billing 中添加支付方式或购买 credits 后再试",
				})
				return
			}
			// 速率限制（429）
			if strings.Contains(msg, "Too Many Requests") || strings.Contains(msg, "rate limit") {
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error":  "rate_limited",
					"detail": "触发速率限制，稍后重试或降低并发/频率",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "llm failed", "detail": msg})
			return
		}

		// 4) 依据真实用量对齐配额（LLM 返回 usage.total_tokens）
		finalRemaining := tr1.GetRemaining()
		if tt := lr.GetTotalTokens(); tt > 0 {
			delta := tt - preReserve // 正数=补扣，负数=回冲
			if delta != 0 {
				actx, acancel := context.WithTimeout(root, 800*time.Millisecond)
				defer acancel()
				if tr2, err := tokenCli.CheckAndInc(actx, &pb.TokenRequest{
					UserId: req.UserID, Tokens: delta,
				}); err == nil {
					finalRemaining = tr2.GetRemaining()
				}
				// 如果对齐失败，不影响本次请求成功返回；remaining 使用预占时的值
			}
		}

		// 5) 保存历史（非阻塞性，失败也不影响本次响应）
		hctx, hcancel := context.WithTimeout(root, 800*time.Millisecond)
		defer hcancel()
		_, _ = historyCli.Save(hctx, &pb.SaveRequest{UserId: req.UserID, Role: "user", Text: req.Text})
		_, _ = historyCli.Save(hctx, &pb.SaveRequest{UserId: req.UserID, Role: "assistant", Text: lr.GetReply()})

		// 6) 返回结果（包含 usage 便于对账/展示）
		c.JSON(http.StatusOK, gin.H{
			"cleaned": fr.GetCleaned(),
			"reply":   lr.GetReply(),
			"usage": gin.H{
				"prompt_tokens":     lr.GetPromptTokens(),
				"completion_tokens": lr.GetCompletionTokens(),
				"total_tokens":      lr.GetTotalTokens(),
			},
			"remaining": finalRemaining,
		})
	})

	// 启动 HTTP 网关
	// 示例：
	// curl -s -X POST http://localhost:8080/chat \
	//   -H 'Content-Type: application/json' \
	//   -d '{"user_id":"u1","text":"Hello   world   from   Go!"}'
	r.Run(":8080")
}
