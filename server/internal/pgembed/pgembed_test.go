package pgembed

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestClearStaleLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "postmaster.pid")
	if err := ClearStaleLock(dir); err != nil {
		t.Fatalf("no lock: %v", err)
	}
	// A pid that cannot exist: stale, removed.
	if err := os.WriteFile(lock, []byte("2147483646\n/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearStaleLock(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("stale lock kept")
	}
	// Our own pid is alive: refuse.
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearStaleLock(dir); err == nil || !strings.Contains(err.Error(), "still runs") {
		t.Fatalf("live lock: %v", err)
	}
}

// TestStartStop downloads Postgres (~25 MB) on the first run, so it only runs
// with FLEETPOCKET_TEST_EMBEDDED_PG=1.
func TestStartStop(t *testing.T) {
	if os.Getenv("FLEETPOCKET_TEST_EMBEDDED_PG") == "" {
		t.Skip("FLEETPOCKET_TEST_EMBEDDED_PG not set")
	}
	dir := t.TempDir()
	if c := os.Getenv("FLEETPOCKET_TEST_EMBEDDED_PG_CACHE"); c != "" {
		// Reuse downloaded binaries between runs.
		_ = os.MkdirAll(filepath.Join(dir, "cache"), 0o700)
		entries, _ := os.ReadDir(c)
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(c, e.Name()))
			_ = os.WriteFile(filepath.Join(dir, "cache", e.Name()), b, 0o600)
		}
	}
	var progressed bool
	db := &DB{Dir: dir, Password: "pw", Progress: func(done, total int64) { progressed = true }}
	start := func() {
		t.Helper()
		if err := db.Start(); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		st, err := store.Open(ctx, db.URL())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("migrations: %v", err)
		}
	}
	start()
	_ = progressed // only true when the cache was empty
	if err := db.Stop(); err != nil {
		t.Fatal(err)
	}
	// A crash leaves postmaster.pid behind; the next start must clear it.
	if err := os.WriteFile(filepath.Join(dir, "data", "postmaster.pid"), []byte("2147483646\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start()
	if err := db.Stop(); err != nil {
		t.Fatal(err)
	}
}
