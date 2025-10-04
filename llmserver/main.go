package main

import (
	"context"
	"log"
	"net"

	pb "chatgpt-demo/chatpb"

	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedLLMServiceServer
}

func (s *server) Generate(ctx context.Context, in *pb.ChatRequest) (*pb.ChatResponse, error) {
	return &pb.ChatResponse{Reply: "Echo: " + in.Text}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50055")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterLLMServiceServer(s, &server{})
	log.Println("LLM stub listening :50055")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
