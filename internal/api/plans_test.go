package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestPlanningWorkspaceEndpointReturnsCanonicalPlansAndDerivedTimeline(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	pastDue := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	futureDue := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	futureTarget := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	completedAt := time.Now().UTC().Add(-12 * time.Hour).Truncate(time.Second)

	ms.plans["u1"] = map[string]store.UserPlan{
		"plan-1": {
			ID:     "plan-1",
			UserID: "u1",
			Title:  "Launch migration",
			Status: "active",
			Milestones: []store.UserPlanMilestone{{
				ID:         "m1",
				Title:      "Backend contract",
				Status:     "doing",
				TargetDate: &futureTarget,
				Tasks: []store.UserPlanTask{
					{ID: "t1", Title: "Add workspace endpoint", Status: "todo", DueAt: &pastDue},
					{ID: "t2", Title: "Document timeline fields", Status: "done", DueAt: &futureDue, CompletedAt: &completedAt},
				},
			}},
			Progress: store.UserPlanProgress{
				MilestoneCount:      1,
				TaskCount:           2,
				CompletedTasks:      1,
				PercentComplete:     50,
				CompletedMilestones: 0,
			},
		},
	}
	ms.plans["u2"] = map[string]store.UserPlan{
		"plan-2": {
			ID:     "plan-2",
			UserID: "u2",
			Title:  "Other user plan",
			Status: "active",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/plans/workspace", nil)
	req.Header.Set("X-User-ID", "u1")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Workspace struct {
			Plans []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"plans"`
			Summary struct {
				PlanCount             int     `json:"plan_count"`
				MilestoneCount        int     `json:"milestone_count"`
				TaskCount             int     `json:"task_count"`
				CompletedTasks        int     `json:"completed_tasks"`
				PercentComplete       float64 `json:"percent_complete"`
				DatedItemCount        int     `json:"dated_item_count"`
				OverdueTaskCount      int     `json:"overdue_task_count"`
				UpcomingTaskCount     int     `json:"upcoming_task_count"`
				UpcomingMilestoneCount int    `json:"upcoming_milestone_count"`
			} `json:"summary"`
			Timeline struct {
				AuthoritativeDateFields []string `json:"authoritative_date_fields"`
				OrderingSemantics       string   `json:"ordering_semantics"`
				HasHeuristicDates       bool     `json:"has_heuristic_dates"`
				Items                   []struct {
					Kind           string     `json:"kind"`
					PlanID         string     `json:"plan_id"`
					MilestoneID    string     `json:"milestone_id"`
					TaskID         string     `json:"task_id"`
					DateKind       string     `json:"date_kind"`
					DateConfidence string     `json:"date_confidence"`
					IsCompleted    bool       `json:"is_completed"`
					IsOverdue      bool       `json:"is_overdue"`
					CompletedAt    *time.Time `json:"completed_at"`
				} `json:"items"`
			} `json:"timeline"`
		} `json:"workspace"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	require.Len(t, body.Workspace.Plans, 1)
	require.Equal(t, "plan-1", body.Workspace.Plans[0].ID)
	require.Equal(t, "Launch migration", body.Workspace.Plans[0].Title)

	assert.Equal(t, 1, body.Workspace.Summary.PlanCount)
	assert.Equal(t, 1, body.Workspace.Summary.MilestoneCount)
	assert.Equal(t, 2, body.Workspace.Summary.TaskCount)
	assert.Equal(t, 1, body.Workspace.Summary.CompletedTasks)
	assert.Equal(t, 50.0, body.Workspace.Summary.PercentComplete)
	assert.Equal(t, 3, body.Workspace.Summary.DatedItemCount)
	assert.Equal(t, 1, body.Workspace.Summary.OverdueTaskCount)
	assert.Equal(t, 0, body.Workspace.Summary.UpcomingTaskCount)
	assert.Equal(t, 1, body.Workspace.Summary.UpcomingMilestoneCount)

	assert.False(t, body.Workspace.Timeline.HasHeuristicDates)
	assert.True(t, slices.Contains(body.Workspace.Timeline.AuthoritativeDateFields, "milestones[].target_date"))
	assert.True(t, strings.Contains(body.Workspace.Timeline.OrderingSemantics, "durable plan model"))
	require.Len(t, body.Workspace.Timeline.Items, 3)

	var overdueTaskFound, completedTaskFound, milestoneFound bool
	for _, item := range body.Workspace.Timeline.Items {
		assert.Equal(t, "plan-1", item.PlanID)
		assert.Equal(t, "authoritative", item.DateConfidence)
		switch {
		case item.Kind == "task" && item.TaskID == "t1":
			overdueTaskFound = true
			assert.Equal(t, "due_at", item.DateKind)
			assert.True(t, item.IsOverdue)
			assert.False(t, item.IsCompleted)
		case item.Kind == "task" && item.TaskID == "t2":
			completedTaskFound = true
			assert.Equal(t, "due_at", item.DateKind)
			assert.False(t, item.IsOverdue)
			assert.True(t, item.IsCompleted)
			require.NotNil(t, item.CompletedAt)
		case item.Kind == "milestone" && item.MilestoneID == "m1":
			milestoneFound = true
			assert.Equal(t, "target_date", item.DateKind)
			assert.False(t, item.IsCompleted)
			assert.False(t, item.IsOverdue)
		}
	}
	assert.True(t, overdueTaskFound)
	assert.True(t, completedTaskFound)
	assert.True(t, milestoneFound)
}

func TestPlanningWorkspaceEndpointTracksPlanCRUDState(t *testing.T) {
	ms := newMockStore()
	svc := service.New(ms, &mockProvider{}, 10)
	router := api.NewRouter(svc)

	dueAt := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	targetDate := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)

	createReq := httptest.NewRequest(http.MethodPost, "/internal/plans", bytes.NewBufferString(`{
		"title":"Planning workspace launch",
		"target":"Support a dedicated planning screen",
		"milestones":[{"id":"m1","title":"Read model","target_date":"`+targetDate+`","tasks":[{"id":"t1","title":"Add planning workspace endpoint","status":"todo","due_at":"`+dueAt+`"}]}]
	}`))
	createReq.Header.Set("X-User-ID", "u1")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	require.Equal(t, http.StatusCreated, createResp.Code)

	var created struct {
		Plan struct {
			ID         string `json:"id"`
			Target     string `json:"target"`
			Milestones []struct {
				ID         string     `json:"id"`
				TargetDate *time.Time `json:"target_date"`
				Tasks      []struct {
					ID    string     `json:"id"`
					DueAt *time.Time `json:"due_at"`
				} `json:"tasks"`
			} `json:"milestones"`
		} `json:"plan"`
	}
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))

	workspaceReq := httptest.NewRequest(http.MethodGet, "/internal/plans/workspace", nil)
	workspaceReq.Header.Set("X-User-ID", "u1")
	workspaceResp := httptest.NewRecorder()
	router.ServeHTTP(workspaceResp, workspaceReq)
	require.Equal(t, http.StatusOK, workspaceResp.Code)

	var workspace struct {
		Workspace struct {
			Plans []struct {
				ID         string `json:"id"`
				Target     string `json:"target"`
				Milestones []struct {
					ID         string     `json:"id"`
					TargetDate *time.Time `json:"target_date"`
					Tasks      []struct {
						ID    string     `json:"id"`
						DueAt *time.Time `json:"due_at"`
					} `json:"tasks"`
				} `json:"milestones"`
			} `json:"plans"`
		} `json:"workspace"`
	}
	require.NoError(t, json.NewDecoder(workspaceResp.Body).Decode(&workspace))
	require.Len(t, workspace.Workspace.Plans, 1)
	assert.Equal(t, created.Plan.ID, workspace.Workspace.Plans[0].ID)
	assert.Equal(t, created.Plan.Target, workspace.Workspace.Plans[0].Target)
	require.Len(t, workspace.Workspace.Plans[0].Milestones, 1)
	assert.Equal(t, created.Plan.Milestones[0].ID, workspace.Workspace.Plans[0].Milestones[0].ID)
	assert.Equal(t, created.Plan.Milestones[0].TargetDate, workspace.Workspace.Plans[0].Milestones[0].TargetDate)
	require.Len(t, workspace.Workspace.Plans[0].Milestones[0].Tasks, 1)
	assert.Equal(t, created.Plan.Milestones[0].Tasks[0].ID, workspace.Workspace.Plans[0].Milestones[0].Tasks[0].ID)
	assert.Equal(t, created.Plan.Milestones[0].Tasks[0].DueAt, workspace.Workspace.Plans[0].Milestones[0].Tasks[0].DueAt)
}
