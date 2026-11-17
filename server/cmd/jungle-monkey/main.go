// Command jungle-monkey runs Jungle Monkey on your own computer (local mode):
// the server, an embedded Postgres and the web app in one binary, talking to
// your Docker. `npx jungle-monkey` runs it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
)

const usage = `Jungle Monkey: AI coding agents on your computer.

usage: jungle-monkey [command] [flags]

commands:
  start             start everything and open the browser (default)
  open              open the web app with a fresh one-time login link
  status            is it running? URL, version, tasks, Docker and Postgres
  stop              stop a running instance
  doctor            check Docker, disk, image, database, credentials, keychain, port
  service install   start at login (launchd / systemd --user / Task Scheduler)
  service uninstall
  logs [-f]         print the log (-f: follow)
  version           print the version

flags (start):
  --no-open         do not open the browser
  --port N          listen on 127.0.0.1:N (remembered)
  --data-dir DIR    data directory (or JM_DATA_DIR)
  --verbose         print the server log to the terminal
`

// exitError carries a specific exit code (2: Docker is not running).
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func main() {
	args := os.Args[1:]
	cmd := "start"
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v" || args[0] == "-h" || args[0] == "--help") {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "start":
		err = runStart(args)
	case "open":
		err = runOpen(args)
	case "status":
		err = runStatus(args)
	case "stop":
		err = runStop(args)
	case "doctor":
		err = runDoctor(args)
	case "service":
		err = runService(args)
	case "logs":
		err = runLogs(args)
	case "version", "--version", "-v":
		fmt.Println("jungle-monkey", config.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, ee.msg)
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, red("✕ ")+err.Error())
		os.Exit(1)
	}
}

// commonFlags are accepted by every command that finds the data directory.
type commonFlags struct {
	dataDir string
	port    int
	noOpen  bool
	verbose bool
	follow  bool
}

func parseFlags(name string, args []string) (*commonFlags, []string, error) {
	f := &commonFlags{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&f.dataDir, "data-dir", "", "data directory")
	fs.IntVar(&f.port, "port", 0, "port")
	fs.BoolVar(&f.noOpen, "no-open", false, "do not open the browser")
	fs.BoolVar(&f.verbose, "verbose", false, "print the server log")
	fs.BoolVar(&f.follow, "f", false, "follow")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	// Local mode is what this binary is; config.Load reads both.
	_ = os.Setenv("MODE", config.ModeLocal)
	if f.dataDir != "" {
		_ = os.Setenv("JM_DATA_DIR", f.dataDir)
	}
	return f, fs.Args(), nil
}

// paths resolves the data directory without starting anything.
func paths() (config.LocalPaths, error) {
	dir, err := config.DefaultDataDir()
	return config.LocalPaths{Root: dir}, err
}
