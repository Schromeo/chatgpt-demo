package main

import (
	"context"
	"net/http"
	"time"

	pb "chatgpt-demo/chatpb"

	"github.com/gin-gonic/gin"
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
	llmConn := mustDial("localhost:50055")
	defer llmConn.Close()

	// gRPC 客户端
	tokenCli := pb.NewTokenServiceClient(tokenConn)
	filterCli := pb.NewFilterServiceClient(filterConn)
	llmCli := pb.NewLLMServiceClient(llmConn)

	// Gin 路由
	r := gin.Default()

	// 请求体
	type chatReq struct {
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}

	// 健康检查（可选）
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// 核心入口：HTTP → (Filter → Token → LLM)
	r.POST("/chat", func(c *gin.Context) {
		var req chatReq
		if err := c.BindJSON(&req); err != nil || req.UserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad json or missing user_id"})
			return
		}

		// 为本次调用设置超时
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// 1) 文本过滤 / 清洗
		fr, err := filterCli.Filter(ctx, &pb.FilterRequest{Text: req.Text})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter failed"})
			return
		}
		if !fr.GetAllowed() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text blocked by filter"})
			return
		}

		// 2) 配额检查（先固定每次 50 tokens；后续可按字数或 tiktoken 计算）
		const perCallTokens = 50
		tr, err := tokenCli.CheckAndInc(ctx, &pb.TokenRequest{
			UserId: req.UserID, Tokens: perCallTokens,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token failed"})
			return
		}
		if !tr.GetAllowed() {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "quota exceeded", "remaining": tr.GetRemaining()})
			return
		}

		// 3) 调用 LLM（目前是 Stub：回声）
		lr, err := llmCli.Generate(ctx, &pb.ChatRequest{
			UserId: req.UserID, Text: fr.GetCleaned(),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "llm failed"})
			return
		}

		// 4) 返回结果
		c.JSON(http.StatusOK, gin.H{
			"cleaned":   fr.GetCleaned(),
			"reply":     lr.GetReply(),
			"remaining": tr.GetRemaining(),
		})
	})

	// 启动 HTTP 网关
	// 访问示例：
	// curl -s -X POST http://localhost:8080/chat \
	//   -H 'Content-Type: application/json' \
	//   -d '{"user_id":"u1","text":"Hello   world   from   Go!"}'
	r.Run(":8080")
}
