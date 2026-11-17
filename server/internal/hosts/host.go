// Package hosts abstracts where a task's workspace and containers live: the
// server's own Docker (Local) or a node connected over a WebSocket (Remote).
// See docs/design/remote-nodes.md.
package hosts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/artifacts"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// ErrOffline is returned by every call to a node that is not connected.
var ErrOffline = errors.New("node is offline")

// ErrLocalRepoOnNode: local repositories live on the server's own disk
// (local mode), so their tasks cannot run on a node.
var ErrLocalRepoOnNode = errors.New("local repositories are not supported for nodes; run the task on this computer")

// ServerID is the placement value clients use for the server itself; tasks
// and projects store it as NULL.
const ServerID = "server"

// AttachmentFile is one attachment of a run: Name is the file name in the
// agent's /attachments folder, Path the file on the server.
type AttachmentFile struct {
	Name string
	Path string
}

// StartSpec describes a runner container. The host adds the workspace and
// attachment mounts and the agent's credentials.
type StartSpec struct {
	TaskID  int64             `json:"task_id"`
	RunID   int64             `json:"run_id"`
	Project string            `json:"project"`
	Image   string            `json:"image"`
	Agent   string            `json:"agent"`
	Env     map[string]string `json:"env"`
	Memory  string            `json:"memory"`
	CPUs    float64           `json:"cpus"`
	User    string            `json:"user"`
	// Attachments mounts the task's attachment folder read-only.
	Attachments bool `json:"attachments"`
	// ExtraHosts are added to the runner's /etc/hosts (local mode: the
	// gateway at host.docker.internal). Not sent to nodes.
	ExtraHosts []string `json:"-"`
}

// LocalDelivery is what DeliverLocal needs: the owner's repository, the
// workspace's task branch and base, the wanted jm/... name, the branch an
// earlier delivery used ("" for the first) and whether a restore rewrote
// the task branch.
type LocalDelivery struct {
	Path    string
	Base    string
	Branch  string
	Want    string
	Current string
	Force   bool
}

// Host is everything the scheduler, the publisher, maintenance and the API
// do with a task's workspace and containers.
type Host interface {
	// ID is "" for the server, else the node id.
	ID() string

	PrepareWorkspace(ctx context.Context, taskID int64, repo workspace.Repo, branch string) error
	UnpushedCommits(ctx context.Context, taskID int64, base, branch string) (int, error)
	Diff(ctx context.Context, taskID int64, base string, limit int) (string, bool, error)
	DiffStat(ctx context.Context, taskID int64, base string) (string, error)
	// Validate checks the diff before a push (workspace.Validate with the
	// host's secrets scanner); rejections wrap workspace.ErrInvalidDiff.
	Validate(ctx context.Context, taskID int64, base string, requireScanner bool) error
	Push(ctx context.Context, taskID int64, repo workspace.Repo, branch string) error
	// Checkpoints (SPEC-server, "Checkpoints"): ref is
	// refs/jm/checkpoints/<run>/<n>, sha the commit it must point at; a
	// missing or moved ref wraps workspace.ErrCheckpointGone.
	CheckpointDiff(ctx context.Context, taskID int64, ref, sha string, limit int) (string, bool, error)
	RestoreCheckpoint(ctx context.Context, taskID int64, ref, sha string) error
	// Diverged: origin/<branch> is not an ancestor of HEAD (a restore rewrote it).
	Diverged(ctx context.Context, taskID int64, branch string) (bool, error)
	// ForcePush pushes with --force-with-lease, only to branches Jungle
	// Monkey created; it returns the replaced remote commit.
	ForcePush(ctx context.Context, taskID int64, repo workspace.Repo, branch string) (string, error)
	RemoveWorkspace(ctx context.Context, taskID int64) error
	// EventLog returns the lines of <workspace>/.fleetpocket/events.jsonl
	// whose seq is greater than afterSeq (nil when there is no log).
	EventLog(ctx context.Context, taskID int64, afterSeq int64) ([][]byte, error)
	// CollectOutputs reads the regular files under <workspace>/.fleetpocket/out
	// (paths relative to it; at most artifacts.MaxFiles+1 files, larger files
	// truncated past artifacts.MaxFileBytes+1 so the caller can reject them).
	CollectOutputs(ctx context.Context, taskID int64) ([]artifacts.File, error)

	// PrepareAttachments makes the files available for the task's
	// /attachments mount.
	PrepareAttachments(ctx context.Context, taskID int64, files []AttachmentFile) error

	HasImage(ctx context.Context, image string) error
	EnsureNetwork(ctx context.Context, taskID int64, project string) error
	EnsureVolume(ctx context.Context, taskID int64, project string) error
	EnsureSidecars(ctx context.Context, taskID int64, project string, services []string) (map[string]string, error)
	StartRunner(ctx context.Context, spec StartSpec) (*runnermgr.Runner, error)
	Attach(ctx context.Context, containerID string) (*runnermgr.Runner, error)
	IsRunning(ctx context.Context, containerID string) (bool, error)
	Wait(ctx context.Context, containerID string) (int, error)
	Stop(ctx context.Context, containerID string, grace time.Duration) error
	Remove(ctx context.Context, containerID string) error
	RemoveAll(ctx context.Context, taskID int64) error
	RemoveSidecars(ctx context.Context, taskID int64) error
	RemoveVolume(ctx context.Context, taskID int64) error
	ListRunners(ctx context.Context) ([]runnermgr.ContainerInfo, error)

	// Local repositories (local mode, docs/SPEC-server.md "Repository
	// sources"). The original folder is only touched by these calls; nodes
	// return ErrLocalRepoOnNode.
	DeliverLocal(ctx context.Context, taskID int64, d LocalDelivery) (*workspace.Delivery, error)
	MergeLocal(ctx context.Context, path, branch string) (*workspace.MergeResult, error)
	CheckoutLocal(ctx context.Context, path, branch string) error
	DiscardLocal(ctx context.Context, path, branch string) error
	TaskIDsWithResources(ctx context.Context) (map[int64]bool, error)
	Ping(ctx context.Context) error
}

// FilterEventLog returns the event lines of r with seq > afterSeq.
func FilterEventLog(r io.Reader, afterSeq int64) ([][]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	var out [][]byte
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var head struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(line, &head) != nil || head.Seq <= afterSeq {
			continue
		}
		out = append(out, append([]byte(nil), line...))
	}
	return out, sc.Err()
}

// ReadOutputs reads the output files of the workspace at ws (none when the
// directory does not exist). Symlinks and other non-regular files are skipped.
func ReadOutputs(ws string) ([]artifacts.File, error) {
	root := filepath.Join(ws, filepath.FromSlash(artifacts.Dir))
	var out []artifacts.File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) > artifacts.MaxFiles {
			return filepath.SkipAll
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(f, artifacts.MaxFileBytes+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, artifacts.File{Path: filepath.ToSlash(rel), Content: string(b)})
		return nil
	})
	return out, err
}
