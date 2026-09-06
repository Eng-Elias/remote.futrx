package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	serviceproject "github.com/futrx-com/remote.futrx.com/internal/service/project"
)

func TestBrokerAdapterScopesRequestsAndDecodesStatus(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var authorization string
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(serviceproject.AgentBrowserInfo{
			Status: serviceproject.AgentBrowserStatusReady,
			Core:   "ready",
			View:   "ready",
		})
	}))
	defer server.Close()

	adapter := NewBrokerAdapter(BrokerConfig{URL: server.URL, SecretFile: secretPath})
	info, err := adapter.Status(context.Background(), "alpha-project")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != serviceproject.AgentBrowserStatusReady || info.Core != "ready" {
		t.Fatalf("status = %#v", info)
	}
	if authorization != "Bearer v1c.YWxwaGEtcHJvamVjdA.A7CLqnMpEgh5IVb7NgreBYliB0xQ_d4eXN6b7gvUhGI" {
		t.Fatalf("authorization = %q", authorization)
	}
	if len(paths) != 1 || paths[0] != "/v1/status" {
		t.Fatalf("paths = %#v", paths)
	}

	connection, err := adapter.Connection(context.Background(), "alpha-project")
	if err != nil {
		t.Fatal(err)
	}
	if connection.URL != server.URL+"/mcp" || !strings.HasPrefix(connection.Token, "v1.") {
		t.Fatalf("connection = %#v", connection)
	}
	if connection.Token != "v1.YWxwaGEtcHJvamVjdA.Lz53nHpENrHBHc_cjqK4A1KqVkuEaoApWsXEG-wyy4c" {
		t.Fatalf("connection token = %q", connection.Token)
	}
	view, err := adapter.ViewTarget(context.Background(), "alpha-project")
	if err != nil {
		t.Fatal(err)
	}
	if view.URL != server.URL+"/view" || view.BearerToken != connection.Token {
		t.Fatalf("view = %#v", view)
	}
	if err := adapter.Delete(context.Background(), "alpha-project"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[1] != "/v1/delete" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestBrokerAdapterRejectsBadConfigurationAndProjectKeys(t *testing.T) {
	adapter := NewBrokerAdapter(BrokerConfig{URL: "not-a-url"})
	if _, err := adapter.Connection(context.Background(), "alpha"); err == nil {
		t.Fatal("expected invalid configuration error")
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter = NewBrokerAdapter(BrokerConfig{URL: "http://127.0.0.1:9323", SecretFile: secretPath})
	if _, err := adapter.Connection(context.Background(), "../other"); err == nil {
		t.Fatal("expected invalid project key error")
	}
}
