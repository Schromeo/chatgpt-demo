package main

import (
	"context"
	"log"
	"net"
	"strings"

	pb "chatgpt-demo/chatpb"

	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedFilterServiceServer
}

// very simple filter: block if contains "foo" or "badword" (case-insensitive)
func (s *server) Filter(ctx context.Context, in *pb.FilterRequest) (*pb.FilterReply, error) {
	raw := strings.TrimSpace(in.Text)
	low := strings.ToLower(raw)

	allowed := !(strings.Contains(low, "foo") || strings.Contains(low, "badword"))

	// 简单清洗：把多余空白压成一个空格
	cleaned := strings.Join(strings.Fields(raw), " ")

	return &pb.FilterReply{
		Allowed: allowed,
		Cleaned: cleaned,
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterFilterServiceServer(s, &server{})
	log.Println("Filter service listening :50052")
	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
