package api_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
)

// Every session route is in docs/openapi.yaml (method and path; parameter
// names may differ).
func TestRoutesDocumented(t *testing.T) {
	b, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	norm := func(p string) string { return param.ReplaceAllString(p, "{}") }
	documented := map[string]bool{}
	path := ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			path = norm(strings.TrimSuffix(strings.TrimSpace(line), ":"))
			continue
		}
		for _, m := range []string{"get", "post", "put", "patch", "delete"} {
			if strings.HasPrefix(line, "    "+m+":") {
				documented[strings.ToUpper(m)+" "+path] = true
			}
		}
	}
	srv := &api.Server{Cfg: &config.Config{}, Auth: &auth.Manager{}, Nodes: &nodes.Service{Registry: &hosts.Registry{}}}
	srv.Handler()
	for _, r := range srv.Routes() {
		method, p, _ := strings.Cut(r.Pattern, " ")
		if !documented[method+" "+norm(p)] {
			t.Errorf("%s is not in docs/openapi.yaml", r.Pattern)
		}
	}
}
