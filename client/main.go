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

func main() {
	// 使用 insecure.NewCredentials() 来关闭 TLS（仅本地 demo）
	cc, err := grpc.Dial("localhost:50055",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer cc.Close()

	c := pb.NewLLMServiceClient(cc)

	text := "你好，世界"
	if len(os.Args) > 1 {
		text = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.Generate(ctx, &pb.ChatRequest{UserId: "u1", Text: text})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("LLM reply =>", resp.Reply)
}
