package main

import (
	"context"
	"log"
	"net"
	"sync"

	pb "chatgpt-demo/chatpb"

	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedTokenServiceServer
	mu    sync.Mutex
	used  map[string]int64
	limit int64
}

func (s *server) CheckAndInc(ctx context.Context, in *pb.TokenRequest) (*pb.TokenReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.used == nil {
		s.used = make(map[string]int64)
	}
	u := s.used[in.UserId] + int64(in.Tokens)
	s.used[in.UserId] = u

	remaining := s.limit - u
	allowed := remaining >= 0
	return &pb.TokenReply{Allowed: allowed, Remaining: remaining}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterTokenServiceServer(s, &server{
		limit: 5000, // 演示：每个用户每天 5000 token（简化：无 TTL）
	})
	log.Println("Token service listening :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
