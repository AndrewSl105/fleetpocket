package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
)

var dockerPingOptions = client.PingOptions{}

// control calls a running instance's control endpoints with the token from
// config.json (readable only by this user).
type control struct {
	base, token string
}

// newControl returns nil when there is no config.json yet (never started).
func newControl(p config.LocalPaths) *control {
	b, err := os.ReadFile(p.StateFile())
	if err != nil {
		return nil
	}
	var st config.LocalState
	if json.Unmarshal(b, &st) != nil || st.Port == 0 {
		return nil
	}
	return &control{base: fmt.Sprintf("http://127.0.0.1:%d", st.Port), token: st.ControlToken}
}

func (c *control) call(method, path string, out any) error {
	return c.send(method, path, nil, out)
}

// send is call with a JSON body.
func (c *control) send(method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Requested-With", "fleetpocket")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *control) ping() bool { return c.call(http.MethodGet, "/api/v1/local/status", nil) == nil }

var errNotRunning = &exitError{code: 3, msg: "Jungle Monkey is not running. Start it with `jungle-monkey`."}

func running() (*control, error) {
	if _, _, err := parseFlags("", nil); err != nil {
		return nil, err
	}
	p, err := paths()
	if err != nil {
		return nil, err
	}
	c := newControl(p)
	if c == nil || !c.ping() {
		return nil, errNotRunning
	}
	return c, nil
}

func runOpen(args []string) error {
	if _, _, err := parseFlags("open", args); err != nil {
		return err
	}
	c, err := running()
	if err != nil {
		return err
	}
	return openAt(c, "")
}

// openAt opens the web app with a fresh login link, landing on next ("" = home).
func openAt(c *control, next string) error {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.call(http.MethodPost, "/api/v1/local/login-code", &out); err != nil {
		return err
	}
	if next != "" {
		out.URL += "&next=" + url.QueryEscape(next)
	}
	if openBrowser(out.URL) != nil {
		fmt.Println(out.URL)
	}
	return nil
}

func runStatus(args []string) error {
	if _, _, err := parseFlags("status", args); err != nil {
		return err
	}
	c, err := running()
	if err != nil {
		return err
	}
	var out map[string]any
	if err := c.call(http.MethodGet, "/api/v1/local/status", &out); err != nil {
		return err
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("%s running\n", green("●"))
	for _, k := range keys {
		fmt.Printf("  %-16s %v\n", k, out[k])
	}
	return nil
}

func runStop(args []string) error {
	if _, _, err := parseFlags("stop", args); err != nil {
		return err
	}
	c, err := running()
	if err != nil {
		return err
	}
	if err := c.call(http.MethodPost, "/api/v1/local/shutdown", nil); err != nil {
		return err
	}
	for range 60 {
		if !c.ping() {
			fmt.Println("Stopped.")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("still running after 30 s")
}

func runLogs(args []string) error {
	f, _, err := parseFlags("logs", args)
	if err != nil {
		return err
	}
	p, err := paths()
	if err != nil {
		return err
	}
	fh, err := os.Open(filepath.Join(p.Logs(), "jungle-monkey.log"))
	if err != nil {
		return err
	}
	defer fh.Close()
	r := bufio.NewReader(fh)
	for {
		line, err := r.ReadString('\n')
		fmt.Print(line)
		if err == io.EOF {
			if !f.follow {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}
