package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// dockerHost finds the user's Docker: DOCKER_HOST, then the current Docker
// context (Docker Desktop, OrbStack and Colima all register one), then the
// well-known sockets. "" lets the client use its platform default.
func dockerHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	cfgDir := os.Getenv("DOCKER_CONFIG")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".docker")
	}
	if h := contextHost(cfgDir, os.Getenv("DOCKER_CONTEXT")); h != "" {
		return h
	}
	if runtime.GOOS == "windows" {
		return "" // npipe:////./pipe/docker_engine
	}
	for _, s := range []string{
		"/var/run/docker.sock",
		filepath.Join(home, ".docker", "run", "docker.sock"),       // Docker Desktop (no admin)
		filepath.Join(home, ".orbstack", "run", "docker.sock"),     // OrbStack
		filepath.Join(home, ".colima", "default", "docker.sock"),   // Colima
		filepath.Join(home, ".rd", "docker.sock"),                  // Rancher Desktop
		filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "docker.sock"), // rootless
	} {
		if fi, err := os.Stat(s); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix://" + s
		}
	}
	return ""
}

// contextHost reads the endpoint of a Docker CLI context: the name comes from
// DOCKER_CONTEXT or config.json's currentContext; its metadata lives in
// contexts/meta/<sha256(name)>/meta.json.
func contextHost(cfgDir, name string) string {
	if name == "" {
		var c struct {
			CurrentContext string `json:"currentContext"`
		}
		b, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
		if err != nil || json.Unmarshal(b, &c) != nil {
			return ""
		}
		name = c.CurrentContext
	}
	if name == "" || name == "default" {
		return ""
	}
	sum := sha256.Sum256([]byte(name))
	b, err := os.ReadFile(filepath.Join(cfgDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(b, &meta) != nil {
		return ""
	}
	return meta.Endpoints["docker"].Host
}
