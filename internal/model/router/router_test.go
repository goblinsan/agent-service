package router_test

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/model/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProvider returns a fixed response and records the request it received.
type stubProvider struct {
	name     string
	lastReq  model.Request
	response model.Response
	err      error
}

func (s *stubProvider) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &s.response, nil
}

func (s *stubProvider) Stream(_ context.Context, req model.Request, onChunk func(string) error) error {
	s.lastReq = req
	if s.err != nil {
		return s.err
	}
	return onChunk(s.response.Content)
}

func TestRouter_DefaultProvider(t *testing.T) {
	def := &stubProvider{name: "default", response: model.Response{Content: "from-default", FinishReason: "stop"}}
	r := router.New(def)

	resp, err := r.Complete(context.Background(), model.Request{Model: "unknown", Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	require.NoError(t, err)
	assert.Equal(t, "from-default", resp.Content)
}

func TestRouter_RuleMatchesByPrefix(t *testing.T) {
	def := &stubProvider{name: "default"}
	llama := &stubProvider{name: "llama", response: model.Response{Content: "from-llama", FinishReason: "stop"}}

	r := router.New(def)
	r.AddProvider("llama", llama)
	r.AddRule(router.Rule{Prefix: "llama", Provider: "llama"})

	resp, err := r.Complete(context.Background(), model.Request{Model: "llama-3", Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	require.NoError(t, err)
	assert.Equal(t, "from-llama", resp.Content)
}

func TestRouter_RuleCatchAll(t *testing.T) {
	def := &stubProvider{name: "default"}
	special := &stubProvider{name: "special", response: model.Response{Content: "catch-all", FinishReason: "stop"}}

	r := router.New(def)
	r.AddProvider("special", special)
	r.AddRule(router.Rule{Prefix: "", Provider: "special"}) // empty prefix catches all

	resp, err := r.Complete(context.Background(), model.Request{Model: "anything"})
	require.NoError(t, err)
	assert.Equal(t, "catch-all", resp.Content)
}

func TestRouter_RuleEvaluatedInOrder(t *testing.T) {
	pA := &stubProvider{name: "a", response: model.Response{Content: "from-a", FinishReason: "stop"}}
	pB := &stubProvider{name: "b", response: model.Response{Content: "from-b", FinishReason: "stop"}}
	def := &stubProvider{name: "default"}

	r := router.New(def)
	r.AddProvider("a", pA)
	r.AddProvider("b", pB)
	r.AddRule(router.Rule{Prefix: "gpt", Provider: "a"})
	r.AddRule(router.Rule{Prefix: "gpt", Provider: "b"}) // should never be reached for "gpt" requests

	resp, err := r.Complete(context.Background(), model.Request{Model: "gpt-4"})
	require.NoError(t, err)
	assert.Equal(t, "from-a", resp.Content)
}

func TestRouter_PrefixMatchCaseInsensitive(t *testing.T) {
	p := &stubProvider{name: "p", response: model.Response{Content: "matched", FinishReason: "stop"}}
	def := &stubProvider{name: "default"}

	r := router.New(def)
	r.AddProvider("p", p)
	r.AddRule(router.Rule{Prefix: "GPT", Provider: "p"})

	resp, err := r.Complete(context.Background(), model.Request{Model: "gpt-3.5"})
	require.NoError(t, err)
	assert.Equal(t, "matched", resp.Content)
}

func TestRouter_NoProviderAvailable(t *testing.T) {
	r := router.New(nil) // no default

	_, err := r.Complete(context.Background(), model.Request{Model: "unknown"})
	require.Error(t, err)
}

func TestRouter_Stream(t *testing.T) {
	p := &stubProvider{name: "p", response: model.Response{Content: "streamed"}}
	r := router.New(p)

	var chunks []string
	err := r.Stream(context.Background(), model.Request{}, func(c string) error {
		chunks = append(chunks, c)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"streamed"}, chunks)
}
