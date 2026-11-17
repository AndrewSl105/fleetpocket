package api_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

const skillBody = "---\nname: Demo\ndescription: Does demo things\n---\nBody.\n"

func TestSkillsAPI(t *testing.T) {
	c, st, secret := setup(t)
	otp, _ := totp.GenerateCode(secret, time.Now())
	c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp})

	if code, body := c.do("POST", "/api/v1/skills", map[string]any{"slug": "demo", "body": "no frontmatter"}); code != 400 {
		t.Fatalf("frontmatter: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/skills", map[string]any{"slug": "demo", "body": skillBody,
		"files": []map[string]string{{"path": "../x", "content": "x"}}}); code != 400 {
		t.Fatalf("path: %d %v", code, body)
	}
	code, sk := c.do("POST", "/api/v1/skills", map[string]any{"slug": "demo", "body": skillBody,
		"files": []map[string]string{{"path": "ref/a.md", "content": "A"}}})
	if code != 201 || sk["name"] != "Demo" || sk["description"] != "Does demo things" {
		t.Fatalf("create: %d %v", code, sk)
	}
	id := jsonNum(int64(sk["id"].(float64)))
	if code, _ := c.do("POST", "/api/v1/skills", map[string]any{"slug": "demo", "body": skillBody}); code != 409 {
		t.Fatalf("duplicate slug: %d", code)
	}
	if code, cp := c.do("POST", "/api/v1/skills/"+id+"/duplicate", nil); code != 201 || cp["slug"] != "demo-copy" {
		t.Fatalf("duplicate: %d %v", code, cp)
	}

	// Export → import round trip under a new slug.
	resp, err := c.http.Get(c.base + "/api/v1/skills/" + id + "/export")
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %v %v", err, resp)
	}
	zip, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("slug", "demo-imported")
	fw, _ := mw.CreateFormFile("file", "demo.zip")
	_, _ = fw.Write(zip)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", c.base+"/api/v1/skills/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Requested-With", "fleetpocket")
	resp, err = c.http.Do(req)
	if err != nil || resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("import: %v %s", err, b)
	}
	resp.Body.Close()
	got, _ := st.SkillsBySlug(t.Context(), []string{"demo-imported"})
	if len(got) != 1 || len(got[0].Files) != 1 || got[0].Files[0].Path != "ref/a.md" || got[0].Body != skillBody {
		t.Fatalf("imported: %+v", got)
	}

	// Project enablement and task overrides.
	c.do("POST", "/api/v1/projects", map[string]any{"id": "sk", "name": "Sk"})
	// With artifact outputs gated off, a project cannot default to them.
	// (They are live now; the gate stays for a build that turns them off.)
	was := tasks.ArtifactsEnabled
	t.Cleanup(func() { tasks.ArtifactsEnabled = was })
	tasks.ArtifactsEnabled = false
	if code, _ := c.do("PATCH", "/api/v1/projects/sk", map[string]any{"default_output": "artifacts"}); code != 400 {
		t.Fatalf("gated default_output: %d", code)
	}
	tasks.ArtifactsEnabled = true
	c.do("POST", "/api/v1/projects/sk/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "base_branch": "main"})
	if code, p := c.do("PATCH", "/api/v1/projects/sk", map[string]any{"skills": []string{"demo", "nope"}, "default_output": "artifacts"}); code != 200 ||
		fmt.Sprint(p["skills"]) != "[demo]" || p["default_output"] != "artifacts" {
		t.Fatalf("project skills: %d %v", code, p)
	}
	if code, body := c.do("PATCH", "/api/v1/projects/sk", map[string]any{"default_output": "zip"}); code != 400 {
		t.Fatalf("bad output: %d %v", code, body)
	}
	defaults, _ := st.DefaultSkillSlugs(t.Context(), "sk")
	if fmt.Sprint(defaults) != "[demo]" {
		t.Fatal(defaults)
	}
	code, task := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "sk", "prompt": "draw", "skills_add": []string{"demo-copy"}, "skills_remove": []string{"demo"}})
	if code != 201 || task["output"] != "artifacts" || fmt.Sprint(task["skills_add"]) != "[demo-copy]" {
		t.Fatalf("task: %d %v", code, task)
	}
	if code, task := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "sk", "prompt": "code", "output": "pr"}); code != 201 || task["output"] != "pr" {
		t.Fatalf("task output override: %d %v", code, task)
	}
	if code, _ := c.do("DELETE", "/api/v1/skills/"+id, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, p := c.do("GET", "/api/v1/projects/sk", nil); code != 200 || fmt.Sprint(p["skills"]) != "[]" {
		t.Fatalf("after delete: %d %v", code, p["skills"])
	}
}
