package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// toServer sends every request to srv, whatever the host.
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.srv.URL)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestClaudeOnline(t *testing.T) {
	var got map[string]any
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			key = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/messages":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &got)
			fmt.Fprint(w, `{"id":"m","type":"message","role":"assistant","model":"claude-haiku-5-5","stop_reason":"end_turn",
				"content":[{"type":"text","text":"Sono Claude."}],"usage":{"input_tokens":5,"output_tokens":3}}`)
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"claude-haiku-5-5","type":"model"},{"id":"claude-sonnet-5-5","type":"model"}],"has_more":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s, _ := localTestService(t)
	s.SetAssistantClient(&http.Client{Transport: toServer{srv}})
	ctx := context.Background()
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "online", Provider: "anthropic"}); err == nil {
		t.Fatal("turned on without a key")
	}
	k := "sk-test"
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "online", Provider: "anthropic", Key: &k}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.AssistantSettings(ctx)
	if a.Model != "claude-haiku-5-5" {
		t.Fatalf("default model %q", a.Model)
	}
	answer, _, err := s.TestAssistant(ctx)
	if err != nil || answer != "Sono Claude." {
		t.Fatalf("answer %q, %v", answer, err)
	}
	if key != "Bearer sk-test" || got["model"] != "claude-haiku-5-5" {
		t.Fatalf("request: key %q, body %v", key, got)
	}
	if oc, _ := got["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Fatalf("effort: %v", got["output_config"])
	}
	ids, err := s.AssistantModels(ctx, "online", "anthropic", "", "")
	if err != nil || len(ids) != 2 {
		t.Fatalf("models %v, %v", ids, err)
	}
}

// Settings saved by 0.6.0-beta.2 have no mode: they are the admin's server.
func TestAssistantSettingsFromBeta2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Ollama in casa."}}]}`)
	}))
	defer srv.Close()
	s, _ := localTestService(t)
	ctx := context.Background()
	if err := s.st.SetSetting(ctx, assistantKey, map[string]any{"enabled": true, "url": srv.URL + "/v1", "model": "qwen2.5:14b"}); err != nil {
		t.Fatal(err)
	}
	if !s.AssistantOn(ctx) {
		t.Fatal("off after the update")
	}
	if answer, _, err := s.TestAssistant(ctx); err != nil || answer != "Ollama in casa." {
		t.Fatalf("answer %q, %v", answer, err)
	}
}

// A key goes only where it was given: an online key never reaches an
// address typed as "your server", nor another online service, and a key
// saved before the scope existed keeps working where it was saved.
func TestAssistantKeyStaysWithItsService(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"m1"}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	s, _ := localTestService(t)
	ctx := context.Background()
	k := "sk-online"
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "online", Provider: "openai", Model: "gpt-x", Key: &k}); err != nil {
		t.Fatal(err)
	}
	// The same address as a home server: no key.
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "server", URL: srv.URL + "/v1", Model: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, has, _ := s.AssistantSettings(ctx); has {
		t.Fatal("online key shown as the server's")
	}
	s.TestAssistant(ctx)
	s.AssistantModels(ctx, "server", "", srv.URL+"/v1", "")
	for _, h := range auth {
		if h != "" {
			t.Fatalf("online key sent to the home server: %q", h)
		}
	}
	// Another online service does not get it either.
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "online", Provider: "mistral", Model: "mistral-x"}); err == nil {
		t.Fatal("mistral turned on with the OpenAI key")
	}

	// A key given for the server is used there.
	auth = nil
	sk := "sk-server"
	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "server", URL: srv.URL + "/v1", Model: "m1", Key: &sk}); err != nil {
		t.Fatal(err)
	}
	s.TestAssistant(ctx)
	if len(auth) == 0 || auth[len(auth)-1] != "Bearer sk-server" {
		t.Fatalf("server key not sent: %q", auth)
	}

	// Saved before 0.6.0-beta.7: no scope, it belongs to the saved settings.
	s.st.DeleteSetting(ctx, assistantKeyFor)
	auth = nil
	s.TestAssistant(ctx)
	if len(auth) == 0 || auth[len(auth)-1] != "Bearer sk-server" {
		t.Fatalf("older key lost: %q", auth)
	}
}
