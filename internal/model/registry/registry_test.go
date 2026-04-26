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
	node := reg.Pick("llama3")
	require.NotNil(t, node)
	assert.Equal(t, "http://n1", node.URL)
}

func TestRegistry_PickReturnsNilWhenNoNodes(t *testing.T) {
	reg := registry.New(nil)
	assert.Nil(t, reg.Pick("llama3"))
}

func TestRegistry_PickSkipsUnhealthyNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
		{Name: "n2", URL: "http://n2"},
	})
	reg.MarkFailed("http://n1")

	node := reg.Pick("")
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_MarkFailedThenMarkHealthy(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
	})
	reg.MarkFailed("http://n1")
	assert.Nil(t, reg.Pick(""))

	reg.MarkHealthy("http://n1")
	assert.NotNil(t, reg.Pick(""))
}

func TestRegistry_PickReturnsNilWhenAllUnhealthy(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1"},
		{Name: "n2", URL: "http://n2"},
	})
	reg.MarkFailed("http://n1")
	reg.MarkFailed("http://n2")
	assert.Nil(t, reg.Pick(""))
}

func TestRegistry_PickByModel_OnlyMatchingNode(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: []string{"gpt-4"}},
		{Name: "n2", URL: "http://n2", Models: []string{"llama3"}},
	})

	node := reg.Pick("llama3")
	require.NotNil(t, node)
	assert.Equal(t, "http://n2", node.URL)
}

func TestRegistry_PickAnyModelWhenNodeHasNoModels(t *testing.T) {
	reg := registry.New([]registry.NodeConfig{
		{Name: "n1", URL: "http://n1", Models: nil},
	})
	// Node with empty Models accepts any model name.
	assert.NotNil(t, reg.Pick("whatever"))
	assert.NotNil(t, reg.Pick(""))
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
		require.Equal(t, "/api/node", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","loaded_model":"llama3.2.gguf"}`))
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.NoError(t, err)

	node := reg.Pick("llama3.2.gguf")
	require.NotNil(t, node)
	assert.Equal(t, []string{"llama3.2.gguf"}, node.Models)
	assert.Nil(t, reg.Pick("mistral.gguf"))
}

func TestRegistry_RefreshFromNodesMarksNodeUnavailableWhenNoModelLoaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"no-model","loaded_model":""}`))
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.NoError(t, err)
	assert.Nil(t, reg.Pick(""))
}

func TestRegistry_RefreshFromNodesMarksNodeUnavailableOnProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	reg := registry.New([]registry.NodeConfig{{Name: "n1", URL: srv.URL}})

	err := reg.RefreshFromNodes(context.Background(), srv.Client())
	require.Error(t, err)
	assert.Nil(t, reg.Pick(""))
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
	assert.Nil(t, reg.Pick(""))
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
	assert.Nil(t, reg.Pick(""))
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
