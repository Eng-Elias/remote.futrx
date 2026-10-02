package applications

import (
	"strings"
	"testing"

	svc "github.com/futrx-com/remote.futrx.com/internal/service/applications"
)

func TestWebRouteRequiresProjectService(t *testing.T) {
	valid := svc.Application{
		ID: "editor", Name: "Editor", Version: "1", Scopes: []svc.Scope{svc.ScopeProject},
		Web:     &svc.ApplicationWeb{Port: 8400, Subdomain: "editor"},
		Service: &svc.ApplicationService{Name: "editor", Command: []string{"/usr/bin/editor"}},
	}
	if err := validateApplication(valid); err != nil {
		t.Fatalf("valid web manifest: %v", err)
	}
	mismatched := valid
	web := *valid.Web
	web.Port = 80
	mismatched.Web = &web
	if err := validateApplication(mismatched); err == nil {
		t.Fatal("accepted privileged web port")
	}
	missingService := valid
	missingService.Service = nil
	if err := validateApplication(missingService); err == nil {
		t.Fatal("accepted web route without a service")
	}
	global := valid
	global.Scopes = []svc.Scope{svc.ScopeGlobal}
	if err := validateApplication(global); err == nil {
		t.Fatal("accepted global web route")
	}
}

func TestWebSubdomainValidation(t *testing.T) {
	for _, label := range []string{"code", "editor-2", "9"} {
		if !svc.ValidWebSubdomain(label) {
			t.Errorf("rejected valid label %q", label)
		}
	}
	for _, label := range []string{"", "code--editor", "Code", "a.b", "-code", "code-", "a_b", "code/evil", "*", strings.Repeat("a", 64)} {
		app := svc.Application{ID: "editor", Name: "Editor", Version: "1", Scopes: []svc.Scope{svc.ScopeProject}, Web: &svc.ApplicationWeb{Port: 8400, Subdomain: label}, Service: &svc.ApplicationService{Name: "editor", Command: []string{"/usr/bin/editor"}}}
		if err := validateApplication(app); err == nil {
			t.Errorf("accepted invalid label %q", label)
		}
	}
}
