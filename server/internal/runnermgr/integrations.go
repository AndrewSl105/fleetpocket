package runnermgr

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// EgressNetworkName is the egress proxy's way out of an internal task network.
func EgressNetworkName(taskID int64) string { return fmt.Sprintf("fleetpocket-egress-%d", taskID) }

// MCPNetworkName holds a task's stdio MCP sidecars: a plain bridge (they
// call their APIs) that the runner is never on.
func MCPNetworkName(taskID int64) string { return fmt.Sprintf("fleetpocket-mcp-%d", taskID) }

// EgressAlias is the proxy's name in the task network.
const EgressAlias = "egress-proxy"

// EgressSpec is the egress proxy sidecar of an allowlist task.
type EgressSpec struct {
	TaskID  int64
	Project string
	Image   string
	// Binary is the proxy executable in the image ("" = the server image's
	// entrypoint, fleetpocket).
	Binary string
	// Binds mount the binary into another image (tests).
	Binds []string
	Env   map[string]string
	// ExtraHosts lets the proxy resolve host.docker.internal (local mode).
	ExtraHosts []string
}

// EgressProxyName is the proxy container's name.
func EgressProxyName(taskID int64) string { return SidecarName(taskID, "egress") }

// EnsureEgressProxy (re)creates the task's egress proxy: on the task
// network as egress-proxy and on its own bridge network to get out. It is
// recreated every run so allowlist changes apply. Returns the proxy URL.
func (m *Manager) EnsureEgressProxy(ctx context.Context, s EgressSpec) (string, error) {
	name := EgressProxyName(s.TaskID)
	_, _ = m.Docker.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	if err := m.ensureBridge(ctx, EgressNetworkName(s.TaskID), s.TaskID, s.Project); err != nil {
		return "", err
	}
	if err := m.ensureImage(ctx, s.Image); err != nil {
		return "", err
	}
	memory, err := ParseMemory(m.SidecarMemory)
	if err != nil {
		return "", err
	}
	bin := cmpOr(s.Binary, "fleetpocket")
	env := []string{}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	pids := int64(256)
	_, err = m.Docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:      s.Image,
			Entrypoint: []string{bin},
			Cmd:        []string{"egress-proxy"},
			Env:        env,
			User:       "1000:1000",
			Labels:     labels(s.TaskID, s.Project, KindSidecar, 0),
			Healthcheck: &container.HealthConfig{
				Test: []string{"CMD", bin, "egress-proxy-health"}, Interval: time.Second, Timeout: 3 * time.Second, Retries: 30,
			},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(EgressNetworkName(s.TaskID)),
			Binds:       s.Binds,
			ExtraHosts:  s.ExtraHosts,
			Resources:   container.Resources{Memory: memory, PidsLimit: &pids},
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			EgressNetworkName(s.TaskID): {},
			NetworkName(s.TaskID):       {Aliases: []string{EgressAlias}},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("egress proxy: %w", err)
	}
	if _, err := m.Docker.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("egress proxy: %w", err)
	}
	if err := m.waitHealthy(ctx, name, 60*time.Second); err != nil {
		return "", fmt.Errorf("egress proxy: %w", err)
	}
	return "http://" + EgressAlias + ":3128", nil
}

func (m *Manager) ensureBridge(ctx context.Context, name string, taskID int64, project string) error {
	res, err := m.Docker.NetworkList(ctx, client.NetworkListOptions{Filters: client.Filters{}.Add("name", name)})
	if err != nil {
		return err
	}
	for _, n := range res.Items {
		if n.Name == name {
			return nil
		}
	}
	_, err = m.Docker.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: labels(taskID, project, KindNetwork, 0)})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}

// StdioSpec is a stdio MCP server container.
type StdioSpec struct {
	TaskID  int64 // 0: a connection test
	Project string
	Name    string
	// Image runs Cmd (with Entrypoint when set).
	Image      string
	Entrypoint []string
	Cmd        []string
	Env        map[string]string
	User       string
}

// MCPSidecarName names a stdio MCP sidecar.
func MCPSidecarName(taskID int64, name string) string {
	if taskID == 0 {
		return "fleetpocket-mcp-test-" + name + "-" + randomHex(4)
	}
	return fmt.Sprintf("fleetpocket-mcp-%d-%s", taskID, name)
}

// StartStdio runs a stdio MCP server and returns its stdout and stdin. Its
// secrets are in its env only; it is on its own network and reachable only
// through this attach stream. stop removes it.
func (m *Manager) StartStdio(ctx context.Context, s StdioSpec) (io.ReadCloser, io.WriteCloser, func(), error) {
	name := MCPSidecarName(s.TaskID, s.Name)
	_, _ = m.Docker.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	netName := MCPNetworkName(s.TaskID)
	if s.TaskID == 0 {
		netName = "bridge"
	} else if err := m.ensureBridge(ctx, netName, s.TaskID, s.Project); err != nil {
		return nil, nil, nil, err
	}
	if err := m.ensureImage(ctx, s.Image); err != nil {
		return nil, nil, nil, err
	}
	memory, err := ParseMemory(m.SidecarMemory)
	if err != nil {
		return nil, nil, nil, err
	}
	env := []string{}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	pids := int64(512)
	lbl := labels(s.TaskID, s.Project, KindMCP, 0)
	if s.TaskID == 0 {
		delete(lbl, LabelTaskID)
	}
	created, err := m.Docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: s.Image, Entrypoint: s.Entrypoint, Cmd: s.Cmd, Env: env, User: s.User, Labels: lbl,
			OpenStdin: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(netName),
			AutoRemove:  s.TaskID == 0,
			Resources:   container.Resources{Memory: memory, PidsLimit: &pids},
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
		},
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mcp sidecar: %w", err)
	}
	stop := func() {
		_, _ = m.Docker.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
	}
	att, err := m.Docker.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		stop()
		return nil, nil, nil, err
	}
	hr := att.HijackedResponse
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(outW, errW, hr.Reader)
		outW.CloseWithError(err)
		errW.CloseWithError(err)
	}()
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := errR.Read(b)
			if n > 0 {
				slog.Debug("mcp sidecar stderr", "name", name, "line", truncate(string(b[:n]), 300))
			}
			if err != nil {
				return
			}
		}
	}()
	if _, err := m.Docker.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		hr.Close()
		stop()
		return nil, nil, nil, err
	}
	in := &stdinWriter{conn: hr}
	return outR, in, func() { hr.Close(); stop() }, nil
}

type stdinWriter struct{ conn client.HijackedResponse }

func (w *stdinWriter) Write(b []byte) (int, error) { return w.conn.Conn.Write(b) }
func (w *stdinWriter) Close() error                { return w.conn.CloseWrite() }
