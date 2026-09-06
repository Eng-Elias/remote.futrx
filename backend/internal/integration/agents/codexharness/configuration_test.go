package codexharness

import (
	"slices"
	"testing"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
)

func TestWithBrowserConnectionSelectsRemoteOrLegacyTransport(t *testing.T) {
	base := []string{"app-server"}
	remote := WithBrowserConnection(base, &agent.BrowserConnection{
		URL:   "http://10.0.0.1:9323/mcp",
		Token: "kept-in-the-environment",
	})
	wantRemote := []string{
		"app-server",
		"-c", `mcp_servers.browser.url="http://10.0.0.1:9323/mcp"`,
		"-c", `mcp_servers.browser.bearer_token_env_var="REMOTE_BROWSER_MCP_TOKEN"`,
	}
	if !slices.Equal(remote, wantRemote) {
		t.Fatalf("remote browser args\n got: %#v\nwant: %#v", remote, wantRemote)
	}
	for _, argument := range remote {
		if argument == "kept-in-the-environment" {
			t.Fatal("browser bearer token leaked into process arguments")
		}
	}

	legacy := WithBrowserConnection(base, nil)
	wantLegacy := AppServerArgs(nil, true)
	if !slices.Equal(legacy, wantLegacy) {
		t.Fatalf("legacy browser args\n got: %#v\nwant: %#v", legacy, wantLegacy)
	}
}
