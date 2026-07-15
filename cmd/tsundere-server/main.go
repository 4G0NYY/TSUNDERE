package main

import (
	"log"

	"github.com/4G0NYY/tsundere/internal/server"
)

func main() {
	cfg := server.LoadConfig()
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("startup failed: %v", err)
	}
	if err := srv.Run(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
