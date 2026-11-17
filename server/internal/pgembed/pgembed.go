// Package pgembed runs the local-mode database: a real Postgres (the schema
// uses jsonb and other Postgres features) started as a child process on a free
// loopback port, with its data under <data dir>/db.
package pgembed

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Version is the Postgres major the deploy compose files also use.
const Version = embeddedpostgres.V17

const (
	user     = "fleetpocket"
	database = "fleetpocket"
)

// Progress reports the first-run download of the Postgres binaries; total is
// -1 when unknown.
type Progress func(done, total int64)

type DB struct {
	// Dir is <data dir>/db.
	Dir      string
	Password string
	// Log receives Postgres' own output (the CLI points it at logs/).
	Log      io.Writer
	Progress Progress

	pg   *embeddedpostgres.EmbeddedPostgres
	port uint32
}

func (d *DB) dataPath() string    { return filepath.Join(d.Dir, "data") }
func (d *DB) cachePath() string   { return filepath.Join(d.Dir, "cache") }
func (d *DB) runtimePath() string { return filepath.Join(d.Dir, "runtime") }

// NeedsDownload reports whether Start will download the binaries first.
func (d *DB) NeedsDownload() bool {
	entries, err := os.ReadDir(d.cachePath())
	return err != nil || len(entries) == 0
}

// Start clears a stale lock, downloads the binaries if needed and starts
// Postgres on a free loopback port.
func (d *DB) Start() error {
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	if err := ClearStaleLock(d.dataPath()); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	d.port = uint32(port)
	logw := d.Log
	if logw == nil {
		logw = io.Discard
	}
	d.pg = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(Version).
		Port(d.port).
		Username(user).
		Password(d.Password).
		Database(database).
		DataPath(d.dataPath()).
		CachePath(d.cachePath()).
		RuntimePath(d.runtimePath()).
		StartParameters(map[string]string{"listen_addresses": "127.0.0.1"}).
		StartTimeout(60 * time.Second).
		Logger(logw))
	if d.Progress != nil && d.NeedsDownload() {
		restore := reportDownloads(d.Progress)
		defer restore()
	}
	return d.pg.Start()
}

// URL is the connection string for store.Open.
func (d *DB) URL() string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, d.Password, d.port, database)
}

func (d *DB) Port() int { return int(d.port) }

// Stop shuts Postgres down cleanly (fast shutdown, no recovery on next start).
func (d *DB) Stop() error {
	if d.pg == nil {
		return nil
	}
	return d.pg.Stop()
}

// ClearStaleLock removes postmaster.pid when the process it names is gone
// (the CLI crashed or the laptop lost power). A live owner is an error:
// another instance uses this data directory.
func ClearStaleLock(dataPath string) error {
	if err := liveLock(dataPath); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(dataPath, "postmaster.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LiveLock reports an error when Postgres of the <data dir>/db directory is
// running (doctor).
func LiveLock(dbDir string) error { return liveLock(filepath.Join(dbDir, "data")) }

func liveLock(dataPath string) error {
	f, err := os.Open(filepath.Join(dataPath, "postmaster.pid"))
	if err != nil {
		return nil
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	_ = f.Close()
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err == nil && pid > 0 && processAlive(pid) {
		return fmt.Errorf("postgres (pid %d) still runs on %s: is another jungle-monkey running?", pid, dataPath)
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// reportDownloads wraps http.DefaultTransport while Start runs:
// embedded-postgres downloads with http.Get and has no progress hook.
var transportMu sync.Mutex

func reportDownloads(p Progress) (restore func()) {
	transportMu.Lock()
	orig := http.DefaultTransport
	http.DefaultTransport = &progressTransport{next: orig, p: p}
	return func() {
		http.DefaultTransport = orig
		transportMu.Unlock()
	}
}

type progressTransport struct {
	next http.RoundTripper
	p    Progress
}

func (t *progressTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil || !strings.HasSuffix(r.URL.Path, ".jar") {
		return resp, err
	}
	resp.Body = &progressBody{ReadCloser: resp.Body, total: resp.ContentLength, p: t.p}
	return resp, nil
}

type progressBody struct {
	io.ReadCloser
	done  atomic.Int64
	total int64
	p     Progress
}

func (b *progressBody) Read(buf []byte) (int, error) {
	n, err := b.ReadCloser.Read(buf)
	b.p(b.done.Add(int64(n)), b.total)
	return n, err
}
