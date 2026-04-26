package registry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/model/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Registry tests ────────────────────────────────────────────────────────────

func TestRegistry_PickReturnsHealthyNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"llama3"}},
	})
	node := reg.Pick("llama3", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, "http://n1", node.URL)
}

func TestRegistry_PickReturnsNilWhenNoNodes(t *testing.T) {
	reg := registry.New(nil)
	assert.Nil(t, reg.Pick("llama3", 0, 0))
}

func TestRegistry_PickSkipsUnhealthyNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
		{Name: "n2", URL: "http://n2"},
	})
	reg.MarkFailed("http://n1")

	node := reg.Pick("", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_MarkFailedThenMarkHealthy(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
	})
	reg.MarkFailed("http://n1")
	assert.Nil(t, reg.Pick("", 0, 0))

	reg.MarkHealthy("http://n1")
	assert.NotNil(t, reg.Pick("", 0, 0))
}

func TestRegistry_PickReturnsNilWhenAllUnhealthy(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
		{Name: "n2", URL: "http://n2"},
	})
	reg.MarkFailed("http://n1")
	reg.MarkFailed("http://n2")
	assert.Nil(t, reg.Pick("", 0, 0))
}

func TestRegistry_PickByModel_OnlyMatchingNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"gpt-4"}},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}},
	})

	node := reg.Pick("llama3", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_PickAnyModelWhenNodeHasNoModels(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: nil},
	})
	// Node with empty Models accepts any model name.
	assert.NotNil(t, reg.Pick("whatever", 0, 0))
	assert.NotNil(t, reg.Pick("", 0, 0))
}

func TestRegistry_PickPrefersLowerLoadHealthyNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"llama3"}, MaxConcurrentRequests: 4, ActiveRequests: 3},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}, MaxConcurrentRequests: 4, ActiveRequests: 1},
	})

	node := reg.Pick("llama3", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_PickPrefersAvailableCapacityOverSaturatedNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"llama3"}, MaxConcurrentRequests: 1, ActiveRequests: 1},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}, MaxConcurrentRequests: 2, ActiveRequests: 1},
	})

	node := reg.Pick("llama3", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_PickSkipsNodeWhenRequestedMaxTokensExceedsNodeLimit(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"llama3"}, MaxTokens: 256},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}, MaxTokens: 1024},
	})

	node := reg.Pick("llama3", 0, 512)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_PickSkipsNodeWhenContextWindowWouldOverflow(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"llama3"}, CtxSize: 600},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}, CtxSize: 2048},
	})

	node := reg.Pick("llama3", 400, 300)
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_Nodes(t *testing.T) {
	cfgs := []registry.NodeConfig{
		{Name: "a", URL: "http://a"},
		{Name: "b", URL: "http://b"},
	}
	reg := registry.New(cfgs)
	nodes := reg.Nodes()
	require.Len(t, nodes, 2)
	assert.Equal(t, cfgs[0], nodes[0])
	assert.Equal(t, cfgs[1], nodes[1])
}

func TestRegistry_RefreshFromNodesUpdatesModelInventory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node":
			_, _ = w.Write([]byte(`{"status":"ok","loaded_model":"llama3.2.gguf","max_concurrent_requests":3,"max_tokens":2048,"ctx_size":8192}`))
		case "/api/metrics":
			_, _ = w.Write([]byte(`{"active_requests":1}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.NoError(t, err)

	node := reg.Pick("llama3.2.gguf", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, []string{"llama3.2.gguf"}, node.Models)
	assert.Equal(t, 3, node.MaxConcurrentRequests)
	assert.Equal(t, 1, node.ActiveRequests)
	assert.Nil(t, reg.Pick("mistral.gguf", 0, 0))
}

func TestRegistry_RefreshFromNodesMarksNodeUnavailableWhenNoModelLoaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node":
			_, _ = w.Write([]byte(`{"status":"no-model","loaded_model":"","max_concurrent_requests":1,"max_tokens":2048,"ctx_size":4096}`))
		case "/api/metrics":
			_, _ = w.Write([]byte(`{"active_requests":0}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.NoError(t, err)
	assert.Nil(t, reg.Pick("", 0, 0))
}

func TestRegistry_RefreshFromNodesMarksNodeUnavailableOnProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/node" {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active_requests":0}`))
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.Error(t, err)
	assert.Nil(t, reg.Pick("", 0, 0))
}

func TestRegistry_RefreshFromNodesKeepsHealthyNodeWhenMetricsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/node" {
			_, _ = w.Write([]byte(`{"status":"ok","loaded_model":"llama3.3.gguf","max_concurrent_requests":2,"max_tokens":2048,"ctx_size":4096}`))
			return
		}
		http.Error(w, "metrics unavailable", http.StatusBadGateway)
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.Error(t, err)
	node := reg.Pick("llama3.3.gguf", 0, 0)
	require.NotNil(t, node)
	assert.Equal(t, 0, node.ActiveRequests)
}

// ── Pool tests ────────────────────────────────────────────────────────────────

type stubProvider struct {
	url     string
	resp    model.Response
	err     error
	lastReq model.Request
}

func (s *stubProvider) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &s.resp, nil
}

func (s *stubProvider) Stream(_ context.Context, req model.Request, onChunk func(string) error) error {
	s.lastReq = req
	if s.err != nil {
		return s.err
	}
	return onChunk(s.resp.Content)
}

func TestPool_CompleteDelegatesToNode(t *testing.T) {
	stub := &stubProvider{url: "http://n1", resp: model.Response{Content: "ok", FinishReason: "stop"}}

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: "http://n1"}})
	pool := registry.NewPool(reg, func(url string) model.Provider { return stub })

	resp, err := pool.Complete(context.Background(), model.Request{Model: "llama3"})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestPool_CompleteMarksNodeFailedOnError(t *testing.T) {
	stub := &stubProvider{url: "http://n1", err: errors.New("backend down")}

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: "http://n1"}})
	pool := registry.NewPool(reg, func(_ string) model.Provider { return stub })

	_, err := pool.Complete(context.Background(), model.Request{})
	require.Error(t, err)

	// Node should now be unhealthy.
	assert.Nil(t, reg.Pick("", 0, 0))
}

func TestPool_CompleteErrorsWhenNoHealthyNode(t *testing.T) {
	reg := registry.New(nil)
	pool := registry.NewPool(reg, func(url string) model.Provider { return nil })

	_, err := pool.Complete(context.Background(), model.Request{Model: "llama3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no healthy node")
}

func TestPool_StreamDelegatesToNode(t *testing.T) {
	stub := &stubProvider{url: "http://n1", resp: model.Response{Content: "streamed"}}

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: "http://n1"}})
	pool := registry.NewPool(reg, func(_ string) model.Provider { return stub })

	var chunks []string
	err := pool.Stream(context.Background(), model.Request{}, func(c string) error {
		chunks = append(chunks, c)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"streamed"}, chunks)
}

func TestPool_StreamMarksNodeFailedOnError(t *testing.T) {
	stub := &stubProvider{url: "http://n1", err: errors.New("stream error")}

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: "http://n1"}})
	pool := registry.NewPool(reg, func(_ string) model.Provider { return stub })

	err := pool.Stream(context.Background(), model.Request{}, func(_ string) error { return nil })
	require.Error(t, err)
	assert.Nil(t, reg.Pick("", 0, 0))
}

func TestPool_StreamErrorsWhenNoHealthyNode(t *testing.T) {
	reg := registry.New(nil)
	pool := registry.NewPool(reg, func(url string) model.Provider { return nil })

	err := pool.Stream(context.Background(), model.Request{}, func(_ string) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no healthy node")
}

func TestPool_FallbackToSecondNodeOnFirstFailing(t *testing.T) {
	stub1 := &stubProvider{url: "http://n1", err: errors.New("down")}
	stub2 := &stubProvider{url: "http://n2", resp: model.Response{Content: "from-n2", FinishReason: "stop"}}

	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
		{Name: "n2", URL: "http://n2"},
	})

	byURL := map[string]model.Provider{
		"http://n1": stub1,
		"http://n2": stub2,
	}
	pool := registry.NewPool(reg, func(url string) model.Provider { return byURL[url] })

	// First call hits n1 which fails; n1 is marked unhealthy.
	_, err := pool.Complete(context.Background(), model.Request{})
	require.Error(t, err)

	// Second call should pick n2.
	resp, err := pool.Complete(context.Background(), model.Request{})
	require.NoError(t, err)
	assert.Equal(t, "from-n2", resp.Content)
}
