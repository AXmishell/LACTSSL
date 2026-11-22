package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"lactssl/internal/model"
	deployplugin "lactssl/internal/plugin/deploy"
)

type matchSiteDeployer struct{}

func (matchSiteDeployer) ID() string        { return "matchpanel" }
func (matchSiteDeployer) Aliases() []string { return nil }
func (matchSiteDeployer) Source() deployplugin.Source {
	return deployplugin.SourceExec
}
func (matchSiteDeployer) Deploy(context.Context, deployplugin.Request) error { return nil }
func (matchSiteDeployer) Info() deployplugin.ExternalDeployer {
	return deployplugin.ExternalDeployer{
		ID:   "matchpanel",
		Name: "Match Panel",
		DeployFields: []deployplugin.ManifestField{
			{Key: "target", Label: "Target", Type: "select", Default: "site"},
			{Key: "sites", Label: "Sites", Type: "text", OptionsSource: "panel_sites", Multiple: true},
		},
	}
}
func (matchSiteDeployer) Plan(context.Context, deployplugin.PlanRequest) (deployplugin.PlanResult, error) {
	return deployplugin.PlanResult{
		Field:    "sites",
		Multiple: true,
		Objects: []deployplugin.PlanObject{
			{Value: "example.com", Name: "example.com", Domains: []string{"example.com", "www.example.com"}},
			{Value: "other.example.net", Name: "other.example.net", Domains: []string{"other.example.net"}},
		},
	}, nil
}

func TestMatchDeployAccountObjects(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := deployplugin.RegisterExternal(matchSiteDeployer{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	account := &model.DeployAccount{
		Name:   "match panel",
		Type:   "matchpanel",
		Config: map[string]string{"api_secret": "secret"},
	}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/deploy-accounts/%d/match", account.ID)
	body := `{"domains":["www.example.com","EXAMPLE.NET"],"config":{"target":"site"}}`
	response := sessionRequest(t, router, http.MethodPost, path, body,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusOK {
		t.Fatalf("match failed: %d %s", response.Code, response.Body.String())
	}
	want := `"values":["example.com"]`
	if !strings.Contains(response.Body.String(), want) {
		t.Fatalf("unexpected match result: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"field":"sites"`) ||
		!strings.Contains(response.Body.String(), `"multiple":true`) {
		t.Fatalf("missing field metadata: %s", response.Body.String())
	}
}

func TestMatchDeployAccountObjectsRequiresDomains(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	if err := deployplugin.RegisterExternal(matchSiteDeployer{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deployplugin.UnregisterSource(deployplugin.SourceExec) })

	account := &model.DeployAccount{Name: "match panel", Type: "matchpanel"}
	if err := st.CreateDeployAccount(account); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/deploy-accounts/%d/match", account.ID)
	response := sessionRequest(t, router, http.MethodPost, path, `{"domains":[]}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty domains should be rejected: %d %s", response.Code, response.Body.String())
	}
}
