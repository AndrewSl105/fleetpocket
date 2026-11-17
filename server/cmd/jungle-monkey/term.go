package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"golang.org/x/term"
)

var tty = term.IsTerminal(int(os.Stderr.Fd()))

func color(code, s string) string {
	if !tty || os.Getenv("NO_COLOR") != "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func red(s string) string   { return color("31", s) }
func green(s string) string { return color("32", s) }
func dim(s string) string   { return color("2", s) }
func bold(s string) string  { return color("1", s) }

// step shows one line with a spinner while work runs, then ✓ or ✕. Progress
// updates the line's suffix (e.g. "42 / 120 MB").
type step struct {
	label string
	mu    sync.Mutex
	extra string
	done  chan struct{}
	wg    sync.WaitGroup
}

func startStep(label string) *step {
	s := &step{label: label, done: make(chan struct{})}
	if !tty {
		fmt.Fprintf(os.Stderr, "  %s…\n", label)
		return s
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		for i := 0; ; i++ {
			s.mu.Lock()
			fmt.Fprintf(os.Stderr, "\r\033[K  %s %s %s", frames[i%len(frames)], s.label, dim(s.extra))
			s.mu.Unlock()
			select {
			case <-s.done:
				return
			case <-time.After(90 * time.Millisecond):
			}
		}
	}()
	return s
}

func (s *step) progress(done, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if total > 0 {
		s.extra = fmt.Sprintf("%d / %d MB", done>>20, total>>20)
	} else {
		s.extra = fmt.Sprintf("%d MB", done>>20)
	}
}

func (s *step) finish(err error) error {
	close(s.done)
	s.wg.Wait()
	if tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", red("✕"), s.label)
		return err
	}
	if tty {
		fmt.Fprintf(os.Stderr, "  %s %s\n", green("✓"), s.label)
	}
	return nil
}

// openBrowser opens url in the default browser; failures only print the URL.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
