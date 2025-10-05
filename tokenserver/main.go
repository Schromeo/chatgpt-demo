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
	used  map[string]int64 // 已用 token 数（按 user_id）
	limit int64            // 当日总配额（简化：无 TTL；后续可接 Redis + 按天重置）
}

func (s *server) CheckAndInc(ctx context.Context, in *pb.TokenRequest) (*pb.TokenReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.used == nil {
		s.used = make(map[string]int64)
	}
	cur := s.used[in.UserId]
	next := cur + int64(in.Tokens)

	// 下限保护：允许负数“回冲”但不低于 0
	if next < 0 {
		next = 0
	}

	// 是否允许本次变更：
	// - 正值（消耗）：只有不超过 limit 才写入并允许
	// - 负值或 0（回冲/对齐）：总是允许并写入
	allowed := true
	if in.Tokens > 0 {
		allowed = next <= s.limit
	}

	if allowed {
		s.used[in.UserId] = next
		// 如果不允许，则不落库，保持 cur 不变
	}

	remaining := s.limit - s.used[in.UserId]
	return &pb.TokenReply{
		Allowed:   allowed,
		Remaining: remaining,
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterTokenServiceServer(s, &server{
		limit: 5000, // 示例：每用户每日 5000（无 TTL；后续可接 Redis + EXPIRE）
	})
	log.Println("Token service listening :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
