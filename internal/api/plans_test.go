package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/service"
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
