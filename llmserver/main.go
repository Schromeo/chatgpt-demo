package main

import (
	"context"
	"log"
	"net"
	"os"

	pb "chatgpt-demo/chatpb"

	"google.golang.org/grpc"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type server struct {
	pb.UnimplementedLLMServiceServer
	client openai.Client
	model  string
}

func newServer() *server {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		log.Fatal("OPENAI_API_KEY is empty")
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &server{
		client: openai.NewClient(option.WithAPIKey(key)),
		model:  model,
	}
}

func (s *server) Generate(ctx context.Context, in *pb.ChatRequest) (*pb.ChatResponse, error) {
	resp, err := s.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(in.Text),
		},
		Model: openai.ChatModel(s.model),
	})
	if err != nil {
		return nil, err
	}

	reply := ""
	if len(resp.Choices) > 0 {
		reply = resp.Choices[0].Message.Content
	}

	// 安全地读取 usage
	var pt, ct, tt int32
	if resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 || resp.Usage.TotalTokens != 0 {
		pt = int32(resp.Usage.PromptTokens)
		ct = int32(resp.Usage.CompletionTokens)
		tt = int32(resp.Usage.TotalTokens)
	}

	return &pb.ChatResponse{
		Reply:            reply,
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      tt,
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50055")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterLLMServiceServer(s, newServer())
	log.Println("LLM (OpenAI) listening :50055")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
