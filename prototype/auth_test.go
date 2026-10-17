package main

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	if askpassMain() {
		return
	}
	os.Exit(m.Run())
}
func TestGitAskpassWithHTTPAuthentication(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	if _, err := runGit(context.Background(), root, "init", "--bare", bare); err != nil {
		t.Fatal(err)
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{Path: bin, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "tester" || p != "private-test-secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	var mu sync.Mutex
	var prompts []string
	ask := func(ctx context.Context, prompt string) (string, error) {
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		if strings.Contains(strings.ToLower(prompt), "username") {
			return "tester", nil
		}
		return "private-test-secret", nil
	}
	out, err := runWriteGit(context.Background(), root, []string{"-c", "credential.helper=", "ls-remote", server.URL + "/remote.git"}, ask)
	if err != nil {
		t.Fatal(out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("prompts=%v", prompts)
	}
	if strings.Contains(out, "private-test-secret") {
		t.Fatal("password leaked into output")
	}
}
