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

func mustDial(addr string) *grpc.ClientConn {
	cc, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	return cc
}

func main() {
	// 准备 gRPC 客户端（网关启动时建好长连）
	filterConn := mustDial("localhost:50052")
	defer filterConn.Close()
	llmConn := mustDial("localhost:50055")
	defer llmConn.Close()

	filterCli := pb.NewFilterServiceClient(filterConn)
	llmCli := pb.NewLLMServiceClient(llmConn)

	r := gin.Default()

	type chatReq struct {
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}

	r.POST("/chat", func(c *gin.Context) {
		var req chatReq
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad json"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// 1) 先做过滤
		fr, err := filterCli.Filter(ctx, &pb.FilterRequest{Text: req.Text})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter failed"})
			return
		}
		if !fr.Allowed {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text blocked by filter"})
			return
		}

		// 2) 再调 LLM（用过滤后的文本）
		lr, err := llmCli.Generate(ctx, &pb.ChatRequest{UserId: req.UserID, Text: fr.Cleaned})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "llm failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"cleaned": fr.Cleaned,
			"reply":   lr.Reply,
		})
	})

	// 启动 HTTP 网关
	r.Run(":8080")
}
