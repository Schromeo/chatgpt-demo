package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "chatgpt-demo/chatpb"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedTokenServiceServer
	rdb   *redis.Client
	limit int64
}

func dayKey(user string) string {
	return fmt.Sprintf("token:%s:%s", user, time.Now().Format("2006-01-02"))
}

// seconds until next 02:00 (给足一整天 + 缓冲，简单起见用 48h)
func ttl() time.Duration { return 48 * time.Hour }

func (s *server) CheckAndInc(ctx context.Context, in *pb.TokenRequest) (*pb.TokenReply, error) {
	key := dayKey(in.UserId)
	delta := int64(in.Tokens)

	// 先增（或回冲），再校验；正向超限则回滚
	val, err := s.rdb.IncrBy(ctx, key, delta).Result()
	if err != nil {
		return nil, err
	}

	// 设置 TTL（仅首次/无 TTL 时）
	_ = s.rdb.ExpireNX(ctx, key, ttl()).Err()

	// 下限保护：如变成负数，纠正回 0
	if val < 0 {
		diff := -val
		if err := s.rdb.IncrBy(ctx, key, diff).Err(); err != nil {
			return nil, err
		}
		val = 0
	}

	// 超限判断（仅正向消耗时）
	allowed := true
	if delta > 0 && val > s.limit {
		allowed = false
		// 回滚刚才的增量
		_ = s.rdb.IncrBy(ctx, key, -delta).Err()
		val, _ = s.rdb.Get(ctx, key).Int64()
	}

	remaining := s.limit - val
	return &pb.TokenReply{Allowed: allowed, Remaining: remaining}, nil
}

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	limit := int64(5000) // 可用环境变量配置
	if v := os.Getenv("DAILY_LIMIT"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); n == 0 || err != nil {
		}
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	s := grpc.NewServer()
	pb.RegisterTokenServiceServer(s, &server{rdb: rdb, limit: limit})

	log.Println("Token (Redis) service @ :50051, limit =", limit, "redis =", addr)
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
