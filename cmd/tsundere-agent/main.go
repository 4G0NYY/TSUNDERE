package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/4G0NYY/tsundere/internal/agent"
)

func main() {
	serverURL := flag.String("server", os.Getenv("TSUNDERE_SERVER_URL"), "TSUNDERE server base URL, e.g. https://status.example.com")
	token := flag.String("token", os.Getenv("TSUNDERE_AGENT_TOKEN"), "agent token (create one in the admin UI)")
	dockerHost := flag.String("docker", envOr("TSUNDERE_DOCKER_HOST", "/var/run/docker.sock"), "docker socket path or tcp:// address; 'off' disables docker checks")
	hostname := flag.String("hostname", os.Getenv("TSUNDERE_HOSTNAME"), "hostname reported to the server; defaults to the OS hostname (in Docker that's the container ID, so set this to the host's name)")
	flag.Parse()

	if *serverURL == "" || *token == "" {
		log.Fatal("both -server and -token are required (or TSUNDERE_SERVER_URL / TSUNDERE_AGENT_TOKEN). I can't monitor thin air, you know.")
	}

	dh := *dockerHost
	if dh == "off" || dh == "none" {
		dh = ""
	}

	a := agent.New(agent.Config{
		ServerURL:  strings.TrimRight(*serverURL, "/"),
		Token:      *token,
		DockerHost: dh,
		Hostname:   strings.TrimSpace(*hostname),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("agent exited: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
