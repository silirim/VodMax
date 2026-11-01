package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Services on the internet, with the person's API key (a subscription such
// as Claude Pro or ChatGPT Plus cannot be used by another app). Claude
// through Anthropic's own API, the others through the OpenAI-compatible
// one.

type AssistantProvider struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Base   string `json:"base"`
	Model  string `json:"model,omitempty"` // suggested
	KeyURL string `json:"key_url"`
	native bool
}

var assistantProviders = []AssistantProvider{
	{ID: "anthropic", Name: "Claude (Anthropic)", Base: "https://api.anthropic.com", Model: "claude-haiku-5-5", KeyURL: "https://platform.claude.com/settings/keys", native: true},
	{ID: "openai", Name: "OpenAI", Base: "https://api.openai.com/v1", KeyURL: "https://platform.openai.com/api-keys"},
	{ID: "mistral", Name: "Mistral", Base: "https://api.mistral.ai/v1", Model: "mistral-small-latest", KeyURL: "https://console.mistral.ai/api-keys"},
	{ID: "gemini", Name: "Google Gemini", Base: "https://generativelanguage.googleapis.com/v1beta/openai", KeyURL: "https://aistudio.google.com/apikey"},
	{ID: "openrouter", Name: "OpenRouter", Base: "https://openrouter.ai/api/v1", KeyURL: "https://openrouter.ai/keys"},
}

func AssistantProviders() []AssistantProvider { return assistantProviders }

func assistantProvider(id string) (AssistantProvider, bool) {
	for _, p := range assistantProviders {
		if p.ID == id {
			return p, true
		}
	}
	return AssistantProvider{}, false
}

func (s *Service) assistantHTTP() *http.Client {
	if s.asst.client != nil {
		return s.asst.client
	}
	return &http.Client{Timeout: assistantTimeout}
}

func (s *Service) anthropicClient(key string) anthropic.Client {
	return anthropic.NewClient(option.WithAPIKey(key), option.WithHTTPClient(s.assistantHTTP()), option.WithMaxRetries(1))
}

// claudeChat asks Claude. The answers are short and simple: low effort.
func (s *Service) claudeChat(ctx context.Context, key, model string, messages []chatMessage) (string, error) {
	params := anthropic.MessageNewParams{
		Model:        anthropic.Model(model),
		MaxTokens:    4000,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
	for _, m := range messages {
		if m.Role == "system" {
			params.System = append(params.System, anthropic.TextBlockParam{Text: m.Content})
		} else {
			params.Messages = append(params.Messages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	client := s.anthropicClient(key)
	resp, err := client.Messages.New(ctx, params)
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && strings.Contains(apiErr.Error(), "effort") {
		// Older models do not take an effort.
		params.OutputConfig = anthropic.OutputConfigParam{}
		resp, err = client.Messages.New(ctx, params)
	}
	if err != nil {
		return "", claudeError(err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", errors.New("Claude non ha voluto rispondere a questa richiesta: riformulala")
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}

func claudeError(err error) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("assistente non raggiungibile: %w", redactURL(err))
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("Claude ha rifiutato la chiave API: controllala (serve una chiave API, non l'abbonamento)")
	case http.StatusNotFound:
		return errors.New("Claude non conosce questo modello: sceglilo dall'elenco")
	case http.StatusTooManyRequests:
		return errors.New("Claude ha ricevuto troppe richieste o il credito è finito: riprova più tardi")
	}
	return fmt.Errorf("Claude ha risposto %d", apiErr.StatusCode)
}

// AssistantModels lists the models a service offers, with the stored key
// when key is empty and it was given for this same service or address.
func (s *Service) AssistantModels(ctx context.Context, mode, provider, base, key string) ([]string, error) {
	if key == "" {
		to := AssistantSettings{Mode: mode, Provider: provider, URL: strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(base), "/"), "/chat/completions")}
		if mode != "online" {
			to.Mode = "server"
		}
		var err error
		if key, err = s.assistantSecret(ctx, to); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if mode == "online" {
		p, ok := assistantProvider(provider)
		if !ok {
			return nil, invalid("servizio sconosciuto")
		}
		if key == "" {
			return nil, invalid("scrivi la chiave API")
		}
		if p.native {
			var ids []string
			client := s.anthropicClient(key)
			pager := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
			for pager.Next() {
				ids = append(ids, pager.Current().ID)
			}
			if err := pager.Err(); err != nil {
				return nil, claudeError(err)
			}
			return ids, nil
		}
		base = p.Base
	}
	base = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(base), "/"), "/chat/completions")
	if base == "" {
		return nil, invalid("scrivi l'indirizzo")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, invalid("indirizzo non valido")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := s.assistantHTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("servizio non raggiungibile: %w", redactURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("il servizio ha rifiutato la chiave API")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("il servizio ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, ErrAssistant
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, strings.TrimPrefix(m.ID, "models/"))
	}
	sort.Strings(ids)
	return ids, nil
}
