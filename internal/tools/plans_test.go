package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/goblinsan/agent-service/internal/store"
)

type fakePlanStore struct {
	store.Store
	plans   []store.UserPlan
	upserts []store.UserPlan
	events  []store.UserEvent
	listErr error
	upErr   error
}

func (f *fakePlanStore) ListActivePlans(ctx context.Context, userID string) ([]store.UserPlan, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.plans, nil
}

func (f *fakePlanStore) UpsertUserPlan(ctx context.Context, p *store.UserPlan) error {
	if f.upErr != nil {
		return f.upErr
	}
	f.upserts = append(f.upserts, *p)
	return nil
}

func (f *fakePlanStore) AppendUserEvent(ctx context.Context, evt *store.UserEvent) error {
	if evt == nil {
		return nil
	}
	f.events = append(f.events, *evt)
	return nil
}

func TestPlanListReturnsPlansForUser(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{
		ID:            "p1",
		UserID:        "u1",
		Title:         "Exit corp",
		Status:        "active",
		Category:      "work",
		Tags:          []string{"career", "pipeline"},
		DataSources:   []string{"github"},
		ReviewCadence: "daily",
		Metrics:       map[string]any{"target_clients": 3},
	}}}
	tool := &PlanListTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	res, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["user_id"] != "u1" {
		t.Errorf("user_id = %v", m["user_id"])
	}
	plans := m["plans"].([]map[string]any)
	if got := len(plans); got != 1 {
		t.Errorf("expected 1 plan, got %d", got)
	}
	if plans[0]["category"] != "work" {
		t.Errorf("expected category work, got %v", plans[0]["category"])
	}
}

func TestPlanListNoUser(t *testing.T) {
	tool := &PlanListTool{Store: &fakePlanStore{}}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.(map[string]any)["note"]; !ok {
		t.Error("expected note for missing user")
	}
}

func TestPlanUpsertCreatesNewWhenIDOmitted(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	res, err := tool.Execute(ctx, map[string]any{"title": "Train for tri"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["created"] != true {
		t.Errorf("expected created=true, got %v", m["created"])
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	if !strings.HasPrefix(fs.upserts[0].ID, "plan-") {
		t.Errorf("expected generated id, got %q", fs.upserts[0].ID)
	}
	if fs.upserts[0].Status != "active" {
		t.Errorf("expected default status active, got %q", fs.upserts[0].Status)
	}
}

func TestPlanUpsertPersistsTypedMetadata(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{
		"title":          "Improve health",
		"category":       "health",
		"tags":           []any{"exercise", "sleep"},
		"data_sources":   `["apple-health","strava"]`,
		"review_cadence": "daily",
		"metrics":        `{"target_workouts_per_week":4,"sleep_hours":8}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := fs.upserts[0]
	if plan.Category != "health" {
		t.Errorf("expected category health, got %q", plan.Category)
	}
	if len(plan.Tags) != 2 || plan.Tags[0] != "exercise" {
		t.Errorf("unexpected tags: %#v", plan.Tags)
	}
	if len(plan.DataSources) != 2 || plan.DataSources[1] != "strava" {
		t.Errorf("unexpected data sources: %#v", plan.DataSources)
	}
	if plan.ReviewCadence != "daily" {
		t.Errorf("expected review cadence daily, got %q", plan.ReviewCadence)
	}
	if got, ok := plan.Metrics["target_workouts_per_week"]; !ok || got.(float64) != 4 {
		t.Errorf("unexpected metrics: %#v", plan.Metrics)
	}
}

func TestPlanUpsertPersistsConnectorsAndDerivesDataSources(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{
		"title": "Improve nutrition consistency",
		"connectors": []any{
			map[string]any{"app": "apple-health", "domain": "health"},
			map[string]any{"app": "loseit", "type": "personal_data_app", "domain": "nutrition", "external_id": "acct-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := fs.upserts[0]
	if len(plan.Connectors) != 2 || plan.Connectors[1].App != "loseit" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if len(plan.DataSources) != 2 || plan.DataSources[0] != "apple-health" || plan.DataSources[1] != "loseit" {
		t.Fatalf("unexpected data sources %#v", plan.DataSources)
	}
}

func TestPlanUpsertUpdatesWhenIDProvided(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	res, err := tool.Execute(ctx, map[string]any{
		"id":     "p1",
		"title":  "Exit corp",
		"status": "done",
		"steps":  []any{map[string]any{"title": "land first client", "status": "done"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["created"] != false {
		t.Error("expected created=false for update")
	}
	if fs.upserts[0].ID != "p1" || fs.upserts[0].Status != "done" || len(fs.upserts[0].Steps) != 1 {
		t.Errorf("unexpected upsert: %+v", fs.upserts[0])
	}
}

func TestPlanUpsertRequiresTitle(t *testing.T) {
	tool := &PlanUpsertTool{Store: &fakePlanStore{}}
	ctx := WithUserID(context.Background(), "u1")
	if _, err := tool.Execute(ctx, map[string]any{}); err == nil {
		t.Error("expected error when title missing")
	}
}

func TestPlanUpsertRequiresUser(t *testing.T) {
	tool := &PlanUpsertTool{Store: &fakePlanStore{}}
	if _, err := tool.Execute(context.Background(), map[string]any{"title": "x"}); err == nil {
		t.Error("expected error when user_id missing")
	}
}

func TestPlanUpsertRejectsMalformedSteps(t *testing.T) {
	tool := &PlanUpsertTool{Store: &fakePlanStore{}}
	ctx := WithUserID(context.Background(), "u1")
	if _, err := tool.Execute(ctx, map[string]any{"title": "x", "steps": "not-an-array"}); err == nil {
		t.Error("expected error for non-array steps")
	}
	if _, err := tool.Execute(ctx, map[string]any{"title": "x", "steps": []any{"not-an-object"}}); err == nil {
		t.Error("expected error for non-object step item")
	}
	if _, err := tool.Execute(ctx, map[string]any{"title": "x", "tags": "not-an-array"}); err == nil {
		t.Error("expected error for malformed tags")
	}
	if _, err := tool.Execute(ctx, map[string]any{"title": "x", "metrics": []any{"not-an-object"}}); err == nil {
		t.Error("expected error for malformed metrics")
	}
}

func TestPlanUpsertAcceptsStepsAsJSONString(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{
		"title": "x",
		"steps": `[{"title":"a","status":"todo"},{"title":"b","status":"done"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.upserts[0].Steps) != 2 || fs.upserts[0].Steps[0]["title"] != "a" {
		t.Errorf("steps not decoded: %+v", fs.upserts[0].Steps)
	}
}

func TestPlanIngestTextDerivesTitleSummaryAndSteps(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanIngestTextTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	res, err := tool.Execute(ctx, map[string]any{
		"text":     "# Launch project manager loop\n\nCoordinate the recurring check-in workflow.\n\n- [ ] Add recurring schedules\n- [x] Fix push delivery\n- Doing: wire goal updates",
		"category": "work",
		"tags":     []any{"assistant", "checkins"},
		"source":   "External PM",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := res.(map[string]any)
	if m["created"] != true {
		t.Errorf("expected created=true, got %v", m["created"])
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	plan := fs.upserts[0]
	if plan.Title != "Launch project manager loop" {
		t.Errorf("unexpected title %q", plan.Title)
	}
	if !strings.Contains(plan.Summary, "Coordinate the recurring check-in workflow.") {
		t.Errorf("unexpected summary %q", plan.Summary)
	}
	if !strings.Contains(plan.Summary, "Imported from External PM.") {
		t.Errorf("expected source marker in summary, got %q", plan.Summary)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Category != "work" {
		t.Errorf("unexpected category %q", plan.Category)
	}
	if len(plan.Tags) != 2 || plan.Tags[0] != "assistant" {
		t.Errorf("unexpected tags %#v", plan.Tags)
	}
	if len(plan.DataSources) != 1 || plan.DataSources[0] != "External PM" {
		t.Errorf("unexpected data sources %#v", plan.DataSources)
	}
	if plan.Steps[0]["status"] != "todo" || plan.Steps[1]["status"] != "done" || plan.Steps[2]["status"] != "doing" {
		t.Errorf("unexpected step statuses: %+v", plan.Steps)
	}
}

func TestPlanIngestTextUsesExistingIDWhenProvided(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanIngestTextTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	res, err := tool.Execute(ctx, map[string]any{
		"id":    "plan-123",
		"title": "Imported roadmap",
		"text":  "Imported roadmap\n\n- Step one",
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.(map[string]any)["created"] != false {
		t.Error("expected created=false when id provided")
	}
	if got := fs.upserts[0].ID; got != "plan-123" {
		t.Errorf("expected plan id plan-123, got %q", got)
	}
}

func TestPlanIngestTextAcceptsConnectorMetadata(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanIngestTextTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"text":      "Weight-loss sprint\n\nTrack calories and protein daily.",
		"connector": map[string]any{"app": "loseit", "domain": "nutrition"},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan := fs.upserts[0]
	if plan.Category != "nutrition" {
		t.Fatalf("expected inferred nutrition category, got %q", plan.Category)
	}
	if len(plan.Connectors) != 1 || plan.Connectors[0].App != "loseit" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if len(plan.DataSources) != 1 || plan.DataSources[0] != "loseit" {
		t.Fatalf("unexpected data sources %#v", plan.DataSources)
	}
}
