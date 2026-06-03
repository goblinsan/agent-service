package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanCRUDEndpoints(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	createReq := httptest.NewRequest(http.MethodPost, "/internal/plans", bytes.NewBufferString(`{
		"title":"Launch migration",
		"target":"Ship hierarchical plans",
		"milestones":[{"title":"Storage","tasks":[{"title":"Add migration","status":"done"},{"title":"Add query methods","status":"todo"}]}]
	}`))
	createReq.Header.Set("X-User-ID", "u1")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	require.Equal(t, http.StatusCreated, createResp.Code)

	var created struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	require.NotEmpty(t, created.Plan.ID)

	listReq := httptest.NewRequest(http.MethodGet, "/internal/plans", nil)
	listReq.Header.Set("X-User-ID", "u1")
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	require.Equal(t, http.StatusOK, listResp.Code)
	assert.Contains(t, listResp.Body.String(), `"title":"Launch migration"`)
	assert.Contains(t, listResp.Body.String(), `"percent_complete":50`)

	getReq := httptest.NewRequest(http.MethodGet, "/internal/plans/"+created.Plan.ID, nil)
	getReq.Header.Set("X-User-ID", "u1")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	require.Equal(t, http.StatusOK, getResp.Code)
	assert.Contains(t, getResp.Body.String(), `"target":"Ship hierarchical plans"`)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/internal/plans/"+created.Plan.ID, nil)
	deleteReq.Header.Set("X-User-ID", "u1")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
}

func TestPlanImportEndpoint(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	payload, err := json.Marshal(map[string]string{
		"source": "endurance-yaml",
		"text":   "title: Endurance Plan\ncategory: health\nmilestones:\n  - title: Week 1\n    tasks:\n      - title: Easy run\n        status: pending\nsteps:\n  - Record heart rate.",
	})
	require.NoError(t, err)
	importReq := httptest.NewRequest(http.MethodPost, "/internal/plans/import", bytes.NewBuffer(payload))
	importReq.Header.Set("X-User-ID", "u1")
	importReq.Header.Set("Content-Type", "application/json")
	importResp := httptest.NewRecorder()
	router.ServeHTTP(importResp, importReq)
	require.Equal(t, http.StatusCreated, importResp.Code)
	assert.Contains(t, importResp.Body.String(), `"title":"Endurance Plan"`)
	assert.Contains(t, importResp.Body.String(), `"category":"health"`)

	listReq := httptest.NewRequest(http.MethodGet, "/internal/plans", nil)
	listReq.Header.Set("X-User-ID", "u1")
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	require.Equal(t, http.StatusOK, listResp.Code)
	assert.Contains(t, listResp.Body.String(), `"title":"Endurance Plan"`)
}

func TestPlanUpsertAndExportRoundTripDurableMetadata(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	createReq := httptest.NewRequest(http.MethodPost, "/internal/plans", bytes.NewBufferString(`{
		"title":"Launch migration",
		"status":"paused",
		"objectives":["Ship dashboard API"],
		"principles":["Keep agent-service canonical"],
		"data_sources":["github"],
		"connectors":[{"app":"github","type":"repo","domain":"work","external_id":"goblinsan/agent-service"}],
		"tracked_metrics":[{"name":"Merged PRs","cadence":"weekly"}],
		"baseline_facts":[{"label":"Current state","value":"Import only"}],
		"success_criteria":["Round-trip YAML"],
		"cadence":[{"day":"mon","activity":"Review backlog"}],
		"supporting_sections":[{"title":"References","kind":"links","items":[{"label":"Spec","kind":"doc","uri":"https://example.com/spec"}]}],
		"milestones":[{"id":"m1","title":"Storage","target_date":"2026-07-01T00:00:00Z","tasks":[{"id":"m1-t1","title":"Add export","status":"done","due_at":"2026-06-10T09:00:00Z","completed_at":"2026-06-11T10:00:00Z"}]}]
	}`))
	createReq.Header.Set("X-User-ID", "u1")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	require.Equal(t, http.StatusCreated, createResp.Code)

	var created struct {
		Plan struct {
			ID         string `json:"id"`
			Connectors []struct {
				ExternalID string `json:"external_id"`
			} `json:"connectors"`
		} `json:"plan"`
	}
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))
	require.NotEmpty(t, created.Plan.ID)
	require.Len(t, created.Plan.Connectors, 1)
	require.Equal(t, "goblinsan/agent-service", created.Plan.Connectors[0].ExternalID)

	exportReq := httptest.NewRequest(http.MethodGet, "/internal/plans/"+created.Plan.ID+"/export", nil)
	exportReq.Header.Set("X-User-ID", "u1")
	exportResp := httptest.NewRecorder()
	router.ServeHTTP(exportResp, exportReq)
	require.Equal(t, http.StatusOK, exportResp.Code)
	assert.Contains(t, exportResp.Header().Get("Content-Type"), "application/yaml")
	assert.Contains(t, exportResp.Header().Get("Content-Disposition"), ".yaml")
	exported := exportResp.Body.String()
	assert.Contains(t, exported, "id: "+created.Plan.ID)
	assert.Contains(t, exported, "status: paused")
	assert.Contains(t, exported, "connectors:")
	assert.Contains(t, exported, "external_id: goblinsan/agent-service")
	assert.Contains(t, exported, "tracked_metrics:")
	assert.Contains(t, exported, "supporting_sections:")
	assert.Contains(t, exported, "target_date: 2026-07-01T00:00:00Z")
	assert.Contains(t, exported, "due_at: 2026-06-10T09:00:00Z")

	importPayload, err := json.Marshal(map[string]string{
		"source": "launch-plan.yaml",
		"text":   exported,
	})
	require.NoError(t, err)
	importReq := httptest.NewRequest(http.MethodPost, "/internal/plans/import", bytes.NewBuffer(importPayload))
	importReq.Header.Set("X-User-ID", "u1")
	importReq.Header.Set("Content-Type", "application/json")
	importResp := httptest.NewRecorder()
	router.ServeHTTP(importResp, importReq)
	require.Equal(t, http.StatusOK, importResp.Code)
	assert.Contains(t, importResp.Body.String(), `"created":false`)

	plans, err := ms.ListActivePlans(importReq.Context(), "u1")
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, created.Plan.ID, plans[0].ID)
	require.Len(t, plans[0].Connectors, 1)
	require.Equal(t, "goblinsan/agent-service", plans[0].Connectors[0].ExternalID)
}

func TestPlanExportEndpointSupportsJSON(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	ms.plans["u1"] = map[string]store.UserPlan{
		"plan-1": {
			ID:     "plan-1",
			UserID: "u1",
			Title:  "Launch migration",
			Status: "active",
			Connectors: []store.PlanConnector{{
				App:        "github",
				ExternalID: "goblinsan/agent-service",
			}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/plans/plan-1/export?format=json", nil)
	req.Header.Set("X-User-ID", "u1")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, resp.Header().Get("Content-Disposition"), ".json")
	assert.True(t, strings.Contains(resp.Body.String(), `"external_id": "goblinsan/agent-service"`) || strings.Contains(resp.Body.String(), `"external_id":"goblinsan/agent-service"`))
}
