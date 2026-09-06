package browser

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/futrx-com/remote.futrx.com/internal/agent"
	serviceproject "github.com/futrx-com/remote.futrx.com/internal/service/project"
)

const (
	brokerTokenVersion    = "v1"
	brokerControlVersion  = "v1c"
	brokerMaxResponseSize = 1 << 20
)

// BrokerConfig selects the host-level shared Chromium runtime. An empty URL
// leaves the legacy per-container browser active.
type BrokerConfig struct {
	URL        string
	SecretFile string
}

func (c BrokerConfig) Enabled() bool { return strings.TrimSpace(c.URL) != "" }

// BrokerAdapter translates the existing container-keyed browser lifecycle to
// the host browser broker. HMAC tokens grant access to exactly one project
// context; agent/view tokens are distinct from lifecycle-control tokens, and
// raw CDP is never exposed to a project container.
type BrokerAdapter struct {
	baseURL    *url.URL
	configErr  error
	secretFile string
	client     *http.Client
}

func NewBrokerAdapter(config BrokerConfig) *BrokerAdapter {
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.URL), "/"))
	if err == nil && (baseURL.Scheme != "http" && baseURL.Scheme != "https" || baseURL.Host == "") {
		err = errors.New("browser broker URL must be an absolute HTTP URL")
	}
	if strings.TrimSpace(config.SecretFile) == "" && err == nil {
		err = errors.New("browser broker secret file is required")
	}
	return &BrokerAdapter{
		baseURL:    baseURL,
		configErr:  err,
		secretFile: config.SecretFile,
		client:     &http.Client{Timeout: agentBrowserReadyTimeout},
	}
}

// Provision checks that the separately supervised broker is reachable. Host
// package/browser convergence belongs to infra, not a project start request.
func (b *BrokerAdapter) Provision(ctx context.Context, _ string) error {
	return b.do(ctx, http.MethodGet, "/health", "", nil)
}

func (b *BrokerAdapter) Start(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/start", containerName, nil)
}

func (b *BrokerAdapter) StartCore(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/start-core", containerName, nil)
}

func (b *BrokerAdapter) StartView(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/start-view", containerName, nil)
}

func (b *BrokerAdapter) Stop(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/stop", containerName, nil)
}

// Delete closes the context and removes its encrypted login state. Normal Stop
// deliberately preserves that state for the next browser start.
func (b *BrokerAdapter) Delete(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/delete", containerName, nil)
}

func (b *BrokerAdapter) StopView(ctx context.Context, containerName string) error {
	return b.do(ctx, http.MethodPost, "/v1/stop-view", containerName, nil)
}

func (b *BrokerAdapter) Running(ctx context.Context, containerName string) (bool, error) {
	info, err := b.Status(ctx, containerName)
	return info.Core == "ready", err
}

func (b *BrokerAdapter) Status(ctx context.Context, containerName string) (serviceproject.AgentBrowserInfo, error) {
	var info serviceproject.AgentBrowserInfo
	if err := b.do(ctx, http.MethodGet, "/v1/status", containerName, &info); err != nil {
		return serviceproject.AgentBrowserInfo{}, err
	}
	return info, nil
}

func (b *BrokerAdapter) Connection(_ context.Context, containerName string) (agent.BrowserConnection, error) {
	token, err := b.token(containerName)
	if err != nil {
		return agent.BrowserConnection{}, err
	}
	return agent.BrowserConnection{URL: b.endpoint("/mcp"), Token: token}, nil
}

func (b *BrokerAdapter) ViewTarget(_ context.Context, containerName string) (serviceproject.AgentBrowserViewTarget, error) {
	token, err := b.token(containerName)
	if err != nil {
		return serviceproject.AgentBrowserViewTarget{}, err
	}
	viewURL := *b.baseURL
	viewURL.Path = strings.TrimRight(viewURL.Path, "/") + "/view"
	viewURL.RawQuery = ""
	viewURL.Fragment = ""
	return serviceproject.AgentBrowserViewTarget{URL: viewURL.String(), BearerToken: token}, nil
}

func (b *BrokerAdapter) do(ctx context.Context, method, path, containerName string, result any) error {
	if b.configErr != nil {
		return b.configErr
	}
	req, err := http.NewRequestWithContext(ctx, method, b.endpoint(path), nil)
	if err != nil {
		return fmt.Errorf("build browser broker request: %w", err)
	}
	if containerName != "" {
		token, tokenErr := b.scopedToken(brokerControlVersion, containerName)
		if tokenErr != nil {
			return tokenErr
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("browser broker %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, brokerMaxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read browser broker %s: %w", path, err)
	}
	if len(body) > brokerMaxResponseSize {
		return fmt.Errorf("browser broker %s response is too large", path)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("browser broker %s returned HTTP %d", path, resp.StatusCode)
	}
	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode browser broker %s: %w", path, err)
		}
	}
	return nil
}

func (b *BrokerAdapter) endpoint(path string) string {
	if b.baseURL == nil {
		return ""
	}
	resolved := *b.baseURL
	resolved.Path = strings.TrimRight(resolved.Path, "/") + path
	resolved.RawQuery = ""
	resolved.Fragment = ""
	return resolved.String()
}

func (b *BrokerAdapter) token(containerName string) (string, error) {
	return b.scopedToken(brokerTokenVersion, containerName)
}

func (b *BrokerAdapter) scopedToken(version, containerName string) (string, error) {
	if b.configErr != nil {
		return "", b.configErr
	}
	if !validBrokerProjectKey(containerName) {
		return "", errors.New("invalid browser project key")
	}
	secret, err := os.ReadFile(b.secretFile)
	if err != nil {
		return "", fmt.Errorf("read browser broker secret: %w", err)
	}
	secret = []byte(strings.TrimSpace(string(secret)))
	if len(secret) < 32 {
		return "", errors.New("browser broker secret must contain at least 32 bytes")
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(containerName))
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, version+"\n"+containerName)
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return version + "." + payload + "." + signature, nil
}

func validBrokerProjectKey(value string) bool {
	if len(value) < 1 || len(value) > 63 || !brokerProjectKeyCharacter(value[0], false) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !brokerProjectKeyCharacter(value[index], true) {
			return false
		}
	}
	return true
}

func brokerProjectKeyCharacter(character byte, allowHyphen bool) bool {
	return character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9' ||
		allowHyphen && character == '-'
}
