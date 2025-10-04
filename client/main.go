package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	pb "chatgpt-demo/chatpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func mustDial(addr string) *grpc.ClientConn {
	cc, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	return cc
}

func main() {
	text := "这是一条最小链路测试"
	if len(os.Args) > 1 {
		text = os.Args[1]
	}

	// 1) 连接 Filter 和 LLM
	filterConn := mustDial("localhost:50052")
	defer filterConn.Close()
	llmConn := mustDial("localhost:50055")
	defer llmConn.Close()

	filterCli := pb.NewFilterServiceClient(filterConn)
	llmCli := pb.NewLLMServiceClient(llmConn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 2) 先 Filter
	fr, err := filterCli.Filter(ctx, &pb.FilterRequest{Text: text})
	if err != nil {
		log.Fatal("filter error:", err)
	}
	if !fr.Allowed {
		fmt.Println("❌ 文本被过滤（包含违禁词）：", text)
		return
	}

	// 3) 再调用 LLM（用清洗后的文本）
	resp, err := llmCli.Generate(ctx, &pb.ChatRequest{UserId: "u1", Text: fr.Cleaned})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✅ 过滤后文本：", fr.Cleaned)
	fmt.Println("LLM reply =>", resp.Reply)
}
