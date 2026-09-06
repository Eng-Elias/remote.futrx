package agent

import "testing"

func TestWithBrowserEnvironmentUsesBackendConnectionWithoutMutatingInput(t *testing.T) {
	base := map[string]string{
		BrowserMCPURLEnvironment:   "https://attacker.invalid/mcp",
		BrowserMCPTokenEnvironment: "attacker-token",
		"KEEP":                     "value",
	}
	connection := &BrowserConnection{
		URL:   "http://10.0.0.1:9323/mcp",
		Token: "project-token",
	}

	got := WithBrowserEnvironment(base, connection)
	if got[BrowserMCPURLEnvironment] != connection.URL || got[BrowserMCPTokenEnvironment] != connection.Token {
		t.Fatalf("browser environment = %#v", got)
	}
	if got["KEEP"] != "value" {
		t.Fatalf("unrelated environment lost: %#v", got)
	}
	if base[BrowserMCPURLEnvironment] != "https://attacker.invalid/mcp" ||
		base[BrowserMCPTokenEnvironment] != "attacker-token" {
		t.Fatalf("input environment mutated: %#v", base)
	}
}
