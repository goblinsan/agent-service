package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goblinsan/agent-service/internal/api"
	"github.com/goblinsan/agent-service/internal/service"
	"github.com/goblinsan/agent-service/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonalDataBatchEndpointAcceptsHealthKitRecords(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := `{
		"source_system":"apple_healthkit",
		"source_app":"Apple Health",
		"schema_version":"apple-health.v1",
		"normalization_version":"gateway-ios.v1",
		"records":[{
			"source_record_type":"health.workout",
			"source_record_subtype":"running",
			"source_record_id":"hk-workout-1",
			"dedupe_key":"hk-workout-1",
			"start_time":"2026-05-28T10:00:00-04:00",
			"end_time":"2026-05-28T10:35:00-04:00",
			"value":3.1,
			"unit":"mile",
			"trust_level":"device_measured"
		}]
	}`

	first := postPersonalDataBatch(t, router, body)
	require.Equal(t, http.StatusOK, first.Code)
	var firstResult store.SourceBatchIngestResult
	require.NoError(t, json.NewDecoder(first.Body).Decode(&firstResult))
	assert.Equal(t, "accepted", firstResult.Status)
	assert.Equal(t, 1, firstResult.Inserted)
	assert.Equal(t, 0, firstResult.Updated)
	assert.Equal(t, 0, firstResult.Rejected)
	assert.Equal(t, "processed", firstResult.ProcessingStatus)
	assert.Len(t, ms.contributions, 1)
	assert.Len(t, ms.rollups, 1)
	assert.Equal(t, "daily_bucket", ms.contributions[0].TargetType)
	assert.Equal(t, "2026-05-28:general_exercise", ms.contributions[0].TargetID)
	assert.Equal(t, "daily-rollup.v1", ms.rollups[0].RollupVersion)

	second := postPersonalDataBatch(t, router, body)
	require.Equal(t, http.StatusOK, second.Code)
	var secondResult store.SourceBatchIngestResult
	require.NoError(t, json.NewDecoder(second.Body).Decode(&secondResult))
	assert.Equal(t, 0, secondResult.Inserted)
	assert.Equal(t, 1, secondResult.Updated)
}

func TestPersonalDataBatchEndpointPartiallyRejectsInvalidRecords(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	body := `{
		"source_system":"apple_healthkit",
		"records":[
			{"source_record_type":"health.nutrition","source_record_id":"protein-1","start_time":"2026-05-28T00:00:00-04:00","value":140,"unit":"g"},
			{"source_record_type":"calendar.event","source_record_id":"wrong-domain"},
			{"source_record_type":"health.nutrition","value":80,"unit":"g"}
		]
	}`

	resp := postPersonalDataBatch(t, router, body)
	require.Equal(t, http.StatusOK, resp.Code)
	var result store.SourceBatchIngestResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "partially_accepted", result.Status)
	assert.Equal(t, 1, result.Inserted)
	assert.Equal(t, 2, result.Rejected)
	assert.Equal(t, "processed", result.ProcessingStatus)
	assert.Len(t, ms.rejectedRecords, 2)
	assert.Len(t, ms.contributions, 1)
}

func postPersonalDataBatch(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/personal-data/batches", bytes.NewBufferString(body))
	req.Header.Set("X-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}
