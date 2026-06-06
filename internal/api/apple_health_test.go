package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppleHealthSummaryEndpointPersistsDataWithoutCreatingPlans(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/apple-health/summary", bytes.NewBufferString(`{
		"date":"2026-05-27",
		"timezone":"America/New_York",
		"activity":{"steps":10420,"exercise_minutes":48,"active_energy_kcal":640},
		"nutrition":{"dietary_energy_kcal":1840,"protein_grams":142}
	}`))
	req.Header.Set("X-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"status":"ok"`)
	assert.Contains(t, resp.Body.String(), `"source_batch"`)
	assert.Len(t, ms.sourceRecords, 5)
	assert.Contains(t, resp.Body.String(), `"matched_plan":false`)

	plans, err := ms.ListActivePlans(req.Context(), "u1")
	require.NoError(t, err)
	require.Len(t, plans, 0)
}

func TestAppleHealthSummaryEndpointRequiresMetrics(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/apple-health/summary", bytes.NewBufferString(`{"date":"2026-05-27"}`))
	req.Header.Set("X-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "activity or nutrition metrics are required")
}
