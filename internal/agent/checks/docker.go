package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// DockerClient talks to the Docker Engine API directly over the socket —
// no docker SDK dependency needed for the handful of endpoints we use.
type DockerClient struct {
	client *http.Client
}

// NewDockerClient accepts a unix socket path (e.g. /var/run/docker.sock) or a
// tcp:// / http:// address.
func NewDockerClient(host string) *DockerClient {
	// Requests always go to the dummy host "docker"; the transport's dialer
	// decides where the connection really goes (unix socket or tcp address).
	tr := &http.Transport{}
	if strings.HasPrefix(host, "tcp://") || strings.HasPrefix(host, "http://") {
		addr := strings.TrimPrefix(strings.TrimPrefix(host, "tcp://"), "http://")
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "2375")
		}
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "tcp", addr)
		}
	} else {
		sock := strings.TrimPrefix(host, "unix://")
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", sock)
		}
	}
	return &DockerClient{
		client: &http.Client{Transport: tr, Timeout: 15 * time.Second},
	}
}

func (d *DockerClient) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("docker API returned HTTP %d for %s", resp.StatusCode, path)
	}
	if out != nil {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

type dockerConfig struct {
	TargetType string `json:"target_type"` // "container" | "service"
	Target     string `json:"target"`      // container name/ID or swarm service name
}

// Docker checks a container's running/health state or a swarm service's
// task counts.
func Docker(ctx context.Context, d *DockerClient, raw json.RawMessage) Result {
	var cfg dockerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Target == "" {
		return down("docker monitor has no target configured")
	}
	start := time.Now()
	var res Result
	if cfg.TargetType == "service" {
		res = d.checkService(ctx, cfg.Target)
	} else {
		res = d.checkContainer(ctx, cfg.Target)
	}
	if res.Up && res.Latency == 0 {
		res.Latency = time.Since(start)
	}
	return res
}

func (d *DockerClient) checkContainer(ctx context.Context, name string) Result {
	var info struct {
		State struct {
			Running bool   `json:"Running"`
			Status  string `json:"Status"`
			Health  *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	code, err := d.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &info)
	if code == http.StatusNotFound {
		return down(fmt.Sprintf("container %q not found", name))
	}
	if err != nil {
		return down("docker API error: " + err.Error())
	}
	if !info.State.Running {
		return down(fmt.Sprintf("container %q is %s", name, info.State.Status))
	}
	if info.State.Health != nil {
		switch info.State.Health.Status {
		case "healthy":
			return up(0, "running, healthy")
		case "starting":
			return down(fmt.Sprintf("container %q health check still starting", name))
		default:
			return down(fmt.Sprintf("container %q is %s", name, info.State.Health.Status))
		}
	}
	return up(0, "running")
}

func (d *DockerClient) checkService(ctx context.Context, name string) Result {
	var svc struct {
		ID   string `json:"ID"`
		Spec struct {
			Name string `json:"Name"`
			Mode struct {
				Replicated *struct {
					Replicas uint64 `json:"Replicas"`
				} `json:"Replicated"`
				Global *struct{} `json:"Global"`
			} `json:"Mode"`
		} `json:"Spec"`
	}
	code, err := d.get(ctx, "/services/"+url.PathEscape(name), &svc)
	if code == http.StatusNotFound {
		return down(fmt.Sprintf("swarm service %q not found", name))
	}
	if err != nil {
		return down("docker API error (is this node a swarm manager?): " + err.Error())
	}

	filters, _ := json.Marshal(map[string][]string{
		"service":       {svc.Spec.Name},
		"desired-state": {"running"},
	})
	var tasks []struct {
		Status struct {
			State string `json:"State"`
		} `json:"Status"`
	}
	if _, err := d.get(ctx, "/tasks?filters="+url.QueryEscape(string(filters)), &tasks); err != nil {
		return down("docker task list failed: " + err.Error())
	}
	running := 0
	for _, t := range tasks {
		if t.Status.State == "running" {
			running++
		}
	}

	if svc.Spec.Mode.Replicated != nil {
		desired := int(svc.Spec.Mode.Replicated.Replicas)
		msg := fmt.Sprintf("%d/%d tasks running", running, desired)
		if desired == 0 {
			return down("service is scaled to 0 replicas")
		}
		if running < desired {
			return down(msg)
		}
		return up(0, msg)
	}
	// Global mode: at least one running task counts as up.
	if running == 0 {
		return down("no running tasks for global service")
	}
	return up(0, fmt.Sprintf("%d tasks running (global)", running))
}

// Inventory lists container names and swarm service names for the mapping
// dropdown in the admin UI. Service listing failing (not a swarm manager)
// is not an error — it just yields an empty list.
func (d *DockerClient) Inventory(ctx context.Context) (map[string]any, error) {
	var containers []struct {
		Names []string `json:"Names"`
	}
	if _, err := d.get(ctx, "/containers/json?all=1", &containers); err != nil {
		return nil, err
	}
	cnames := []string{}
	for _, c := range containers {
		if len(c.Names) > 0 {
			cnames = append(cnames, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	sort.Strings(cnames)

	snames := []string{}
	var services []struct {
		Spec struct {
			Name string `json:"Name"`
		} `json:"Spec"`
	}
	if _, err := d.get(ctx, "/services", &services); err == nil {
		for _, s := range services {
			snames = append(snames, s.Spec.Name)
		}
		sort.Strings(snames)
	}

	return map[string]any{"containers": cnames, "services": snames}, nil
}
