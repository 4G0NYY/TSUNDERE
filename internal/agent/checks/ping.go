package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type pingConfig struct {
	Host       string `json:"host"`
	TimeoutSec int    `json:"timeout_sec"`
}

// pingTimeRe matches "time=12.3 ms", "time<1ms", "Zeit=12ms" etc. across
// ping implementations and locales.
var pingTimeRe = regexp.MustCompile(`[a-zA-Z]+[=<]([0-9.]+)\s?ms`)

// Ping shells out to the system ping binary — it works unprivileged everywhere
// (raw ICMP sockets would need CAP_NET_RAW), and busybox/iputils/Windows ping
// are all handled by the same output parsing.
func Ping(ctx context.Context, raw json.RawMessage) Result {
	var cfg pingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || strings.TrimSpace(cfg.Host) == "" {
		return down("ping monitor has no host configured")
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 5
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", "1", "-w", strconv.Itoa(cfg.TimeoutSec*1000), cfg.Host)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(cfg.TimeoutSec), cfg.Host)
	}

	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		msg := fmt.Sprintf("ping to %s failed", cfg.Host)
		if len(out) > 0 {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			msg += ": " + strings.TrimSpace(lines[len(lines)-1])
		}
		return down(msg)
	}

	latency := elapsed
	if m := pingTimeRe.FindSubmatch(out); m != nil {
		if ms, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			latency = time.Duration(ms * float64(time.Millisecond))
		}
	}
	return up(latency, "")
}
