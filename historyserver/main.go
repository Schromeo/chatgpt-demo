package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	pb "chatgpt-demo/chatpb"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedHistoryServiceServer
	db  *sql.DB
	rdb *redis.Client
}

func hkey(user string) string { return "history:" + user }

const cacheN = 40

type item struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (s *server) Save(ctx context.Context, in *pb.SaveRequest) (*pb.SaveReply, error) {
	// 1) 持久化到 MySQL
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO chat_history(user_id, role, text) VALUES(?,?,?)",
		in.UserId, in.Role, in.Text)
	if err != nil {
		return &pb.SaveReply{Ok: false}, err
	}

	// 2) 写入 Redis 最近 N 条（LPUSH + LTRIM）
	if s.rdb != nil {
		b, _ := json.Marshal(item{Role: in.Role, Text: in.Text})
		pipe := s.rdb.TxPipeline()
		pipe.LPush(ctx, hkey(in.UserId), b)
		pipe.LTrim(ctx, hkey(in.UserId), 0, cacheN-1)
		pipe.Expire(ctx, hkey(in.UserId), 24*time.Hour)
		_, _ = pipe.Exec(ctx)
	}

	return &pb.SaveReply{Ok: true}, nil
}

func (s *server) List(ctx context.Context, in *pb.ListRequest) (*pb.ListReply, error) {
	limit := int64(in.Limit)
	if limit <= 0 {
		limit = 20
	}

	// 1) 先查 Redis
	if s.rdb != nil {
		raws, err := s.rdb.LRange(ctx, hkey(in.UserId), 0, limit-1).Result()
		if err == nil && len(raws) > 0 {
			items := make([]*pb.HistoryItem, 0, len(raws))
			for _, r := range raws {
				var it item
				if json.Unmarshal([]byte(r), &it) == nil {
					items = append(items, &pb.HistoryItem{Role: it.Role, Text: it.Text})
				}
			}
			return &pb.ListReply{Items: items}, nil
		}
	}

	// 2) Miss：查 MySQL（倒序取最近）
	rows, err := s.db.QueryContext(ctx,
		"SELECT role, text FROM chat_history WHERE user_id=? ORDER BY id DESC LIMIT ?",
		in.UserId, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*pb.HistoryItem
	for rows.Next() {
		var role, text string
		_ = rows.Scan(&role, &text)
		items = append(items, &pb.HistoryItem{Role: role, Text: text})
	}
	return &pb.ListReply{Items: items}, nil
}

func main() {
	// MySQL 连接
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		user := getenv("MYSQL_USER", "root")
		pass := getenv("MYSQL_PASSWORD", "root")
		host := getenv("MYSQL_ADDR", "localhost:3306")
		dsn = fmt.Sprintf("%s:%s@tcp(%s)/chatdb?parseTime=true&charset=utf8mb4,utf8", user, pass, host)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}

	// Redis（可选，但推荐）
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	lis, err := net.Listen("tcp", ":50054")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterHistoryServiceServer(s, &server{db: db, rdb: rdb})

	log.Println("History service @ :50054, mysql =", dsn, "redis =", redisAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
