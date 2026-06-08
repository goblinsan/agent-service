package tools

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

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

func (f *fakePlanStore) GetUserPlan(ctx context.Context, userID, planID string) (*store.UserPlan, error) {
	for i := range f.plans {
		if f.plans[i].ID == planID && f.plans[i].UserID == userID {
			p := f.plans[i]
			return &p, nil
		}
	}
	return nil, store.ErrNotFound
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

func TestPlanUpsertReusesExistingPlanWithSameTitle(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{
		ID:     "plan-existing",
		UserID: "u1",
		Title:  "Train for tri",
		Status: "active",
	}}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	res, err := tool.Execute(ctx, map[string]any{"title": "Train for tri"})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["created"] != false {
		t.Errorf("expected created=false, got %v", res.(map[string]any)["created"])
	}
	if got := fs.upserts[0].ID; got != "plan-existing" {
		t.Fatalf("expected existing id, got %q", got)
	}
}

func TestPlanUpsertPrefersRicherDuplicateByTitle(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{
		{
			ID:     "plan-thin",
			UserID: "u1",
			Title:  "Train for tri",
			Status: "active",
		},
		{
			ID:         "plan-rich",
			UserID:     "u1",
			Title:      "Train for tri",
			Status:     "active",
			Vision:     "Finish strong.",
			Objectives: []string{"Swim", "Bike", "Run"},
			Milestones: []store.UserPlanMilestone{{ID: "m1", Title: "Week 1"}},
		},
	}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{"title": "Train for tri"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fs.upserts[0].ID; got != "plan-rich" {
		t.Fatalf("expected richest plan id, got %q", got)
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

func TestPlanUpsertPersistsMilestonesAndRollup(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{
		"title":  "Exit corp",
		"vision": "Operate a profitable independent studio",
		"target": "$1k/day by Q4",
		"milestones": []any{
			map[string]any{
				"title": "Acquire pipeline",
				"tasks": []any{
					map[string]any{"title": "Publish offer", "status": "done"},
					map[string]any{"title": "Close first client", "status": "todo"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := fs.upserts[0]
	if plan.Vision == "" || plan.Target == "" {
		t.Fatalf("expected vision/target to persist: %+v", plan)
	}
	if len(plan.Milestones) != 1 || len(plan.Milestones[0].Tasks) != 2 {
		t.Fatalf("unexpected milestones: %+v", plan.Milestones)
	}
	if got := plan.Progress.PercentComplete; got != 50 {
		t.Fatalf("expected 50%% completion, got %v", got)
	}
}

func TestPlanUpsertParsesSchedulingSemantics(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	scheduledDate := "2026-06-01T09:00:00Z"
	startDate := "2026-06-02T09:00:00Z"
	targetDate := "2026-07-01T00:00:00Z"
	endDate := "2026-07-05T00:00:00Z"
	scheduledAt := "2026-06-03T09:00:00Z"
	startAt := "2026-06-04T09:00:00Z"
	targetAt := "2026-06-20T09:00:00Z"
	dueAt := "2026-06-21T09:00:00Z"
	taskEndAt := "2026-06-22T09:00:00Z"
	_, err := tool.Execute(ctx, map[string]any{
		"title": "Scheduling Plan",
		"milestones": []any{
			map[string]any{
				"id":             "m1",
				"title":          "Milestone 1",
				"scheduled_date": scheduledDate,
				"start_date":     startDate,
				"target_date":    targetDate,
				"end_date":       endDate,
				"depends_on":     []any{"m0"},
				"sequence":       float64(2),
				"tasks": []any{
					map[string]any{
						"id":           "t1",
						"title":        "Task 1",
						"scheduled_at": scheduledAt,
						"start_at":     startAt,
						"target_at":    targetAt,
						"due_at":       dueAt,
						"end_at":       taskEndAt,
						"depends_on":   []any{"t0"},
						"sequence":     float64(3),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := fs.upserts[0]
	if len(plan.Milestones) != 1 {
		t.Fatalf("expected one milestone, got %#v", plan.Milestones)
	}
	m := plan.Milestones[0]
	if m.ScheduledDate == nil || m.StartDate == nil || m.TargetDate == nil || m.EndDate == nil {
		t.Fatalf("expected milestone dates, got %#v", m)
	}
	if !reflect.DeepEqual(m.DependsOn, []string{"m0"}) {
		t.Fatalf("unexpected milestone depends_on %#v", m.DependsOn)
	}
	if m.Sequence != 2 {
		t.Fatalf("unexpected milestone sequence %d", m.Sequence)
	}
	if len(m.Tasks) != 1 {
		t.Fatalf("expected one task, got %#v", m.Tasks)
	}
	task := m.Tasks[0]
	if task.ScheduledAt == nil || task.StartAt == nil || task.TargetAt == nil || task.DueAt == nil || task.EndAt == nil {
		t.Fatalf("expected task dates, got %#v", task)
	}
	if !reflect.DeepEqual(task.DependsOn, []string{"t0"}) {
		t.Fatalf("unexpected task depends_on %#v", task.DependsOn)
	}
	if task.Sequence != 3 {
		t.Fatalf("unexpected task sequence %d", task.Sequence)
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

func TestPlanIngestTextReusesExistingPlanWithSameTitle(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{
		ID:     "plan-existing",
		UserID: "u1",
		Title:  "Imported roadmap",
		Status: "active",
	}}}
	tool := &PlanIngestTextTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	res, err := tool.Execute(ctx, map[string]any{
		"title": "Imported roadmap",
		"text":  "Imported roadmap\n\n- Step one",
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.(map[string]any)["created"] != false {
		t.Error("expected created=false when title already exists")
	}
	if got := fs.upserts[0].ID; got != "plan-existing" {
		t.Errorf("expected plan id plan-existing, got %q", got)
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

func TestPlanIngestTextBuildsMilestonesForBackwardCompatibility(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanIngestTextTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")
	_, err := tool.Execute(ctx, map[string]any{
		"title": "Roadmap",
		"text":  "Roadmap\n\n- [x] Setup store\n- [ ] Add APIs",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := fs.upserts[0]
	if len(plan.Milestones) != 1 || len(plan.Milestones[0].Tasks) != 2 {
		t.Fatalf("expected milestones derived from steps, got %+v", plan.Milestones)
	}
	if plan.Progress.TaskCount != 2 || plan.Progress.CompletedTasks != 1 {
		t.Fatalf("unexpected progress %+v", plan.Progress)
	}
}

func TestBuildUserPlanFromDocumentRejectsMalformedStructuredImport(t *testing.T) {
	_, _, _, err := BuildUserPlanFromDocument("u1", map[string]any{
		"title":  "endurance.yaml",
		"source": "endurance.yaml",
		"text":   "title: Endurance Plan\nmilestones:\n  - title: Week 1\n    tasks:\n      - title: Monday: 3 easy miles\n",
	})
	if err == nil {
		t.Fatal("expected malformed YAML import to fail")
	}
	if !strings.Contains(err.Error(), "Fix the YAML/JSON formatting") {
		t.Fatalf("expected helpful formatting message, got %q", err.Error())
	}
}

func TestBuildUserPlanFromDocumentKeepsStructuredFieldsForValidYAML(t *testing.T) {
	plan, _, _, err := BuildUserPlanFromDocument("u1", map[string]any{
		"title":  "endurance.yaml",
		"source": "endurance.yaml",
		"text":   "title: Endurance Plan\ncategory: health\nobjectives:\n  - Build aerobic base\ntracked_metrics:\n  - name: Distance\n    cadence: daily\nsupporting_sections:\n  - title: Guidance\n    kind: note\n    items:\n      - label: Easy means easy\n        kind: note\n        content: Stay controlled.\nmilestones:\n  - title: Week 1\n    tasks:\n      - title: \"Monday: 3 easy miles\"\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Category != "health" {
		t.Fatalf("expected category health, got %q", plan.Category)
	}
	if len(plan.Objectives) != 1 || plan.Objectives[0] != "Build aerobic base" {
		t.Fatalf("unexpected objectives: %#v", plan.Objectives)
	}
	if len(plan.TrackedMetrics) != 1 || plan.TrackedMetrics[0].Name != "Distance" {
		t.Fatalf("unexpected tracked metrics: %#v", plan.TrackedMetrics)
	}
	if len(plan.SupportingSections) != 1 || plan.SupportingSections[0].Title != "Guidance" {
		t.Fatalf("unexpected supporting sections: %#v", plan.SupportingSections)
	}
	if len(plan.Milestones) != 1 || len(plan.Milestones[0].Tasks) != 1 {
		t.Fatalf("unexpected milestones: %#v", plan.Milestones)
	}
}

func TestBuildUserPlanFromDocumentDoesNotOverwriteStructuredDataSourcesWithImportSource(t *testing.T) {
	plan, _, _, err := BuildUserPlanFromDocument("u1", map[string]any{
		"title":  "endurance.yaml",
		"source": "restoration",
		"text": "title: Endurance Plan\n" +
			"data_sources:\n" +
			"  - healthfit\n" +
			"  - loseit\n" +
			"milestones:\n" +
			"  - title: Week 1\n" +
			"    tasks:\n" +
			"      - title: \"Monday: 3 easy miles\"\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.DataSources) != 2 || plan.DataSources[0] != "healthfit" || plan.DataSources[1] != "loseit" {
		t.Fatalf("unexpected data sources %#v", plan.DataSources)
	}
}

func TestBuildUserPlanFromDocumentPreservesStructuredIdentityConnectorsAndDates(t *testing.T) {
	scheduledDate := "2026-06-01T00:00:00Z"
	startDate := "2026-06-02T00:00:00Z"
	targetDate := "2026-07-01T00:00:00Z"
	endDate := "2026-07-10T00:00:00Z"
	scheduledAt := "2026-06-05T09:00:00Z"
	startAt := "2026-06-06T09:00:00Z"
	targetAt := "2026-06-09T09:00:00Z"
	dueAt := "2026-06-10T09:00:00Z"
	endAt := "2026-06-12T09:00:00Z"
	completedAt := "2026-06-11T10:00:00Z"

	plan, _, _, err := BuildUserPlanFromDocument("u1", map[string]any{
		"source": "launch-plan.yaml",
		"text": "id: plan-launch\n" +
			"title: Launch Plan\n" +
			"status: paused\n" +
			"connectors:\n" +
			"  - app: github\n" +
			"    type: repo\n" +
			"    domain: work\n" +
			"    external_id: goblinsan/agent-service\n" +
			"milestones:\n" +
			"  - id: milestone-1\n" +
			"    title: Ship API\n" +
			"    scheduled_date: " + scheduledDate + "\n" +
			"    start_date: " + startDate + "\n" +
			"    target_date: " + targetDate + "\n" +
			"    end_date: " + endDate + "\n" +
			"    depends_on:\n" +
			"      - milestone-0\n" +
			"    sequence: 2\n" +
			"    tasks:\n" +
			"      - id: task-1\n" +
			"        title: Add export route\n" +
			"        scheduled_at: " + scheduledAt + "\n" +
			"        start_at: " + startAt + "\n" +
			"        target_at: " + targetAt + "\n" +
			"        due_at: " + dueAt + "\n" +
			"        end_at: " + endAt + "\n" +
			"        depends_on:\n" +
			"          - task-0\n" +
			"        sequence: 3\n" +
			"        completed_at: " + completedAt + "\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "plan-launch" {
		t.Fatalf("expected imported id to round-trip, got %q", plan.ID)
	}
	if plan.Status != "paused" {
		t.Fatalf("expected imported status, got %q", plan.Status)
	}
	if len(plan.Connectors) != 1 || plan.Connectors[0].ExternalID != "goblinsan/agent-service" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if len(plan.Milestones) != 1 || plan.Milestones[0].TargetDate == nil {
		t.Fatalf("expected milestone target date, got %#v", plan.Milestones)
	}
	if got := plan.Milestones[0].TargetDate.UTC().Format(time.RFC3339); got != targetDate {
		t.Fatalf("unexpected target date %q", got)
	}
	if got := plan.Milestones[0].ScheduledDate.UTC().Format(time.RFC3339); got != scheduledDate {
		t.Fatalf("unexpected scheduled_date %q", got)
	}
	if got := plan.Milestones[0].StartDate.UTC().Format(time.RFC3339); got != startDate {
		t.Fatalf("unexpected start_date %q", got)
	}
	if got := plan.Milestones[0].EndDate.UTC().Format(time.RFC3339); got != endDate {
		t.Fatalf("unexpected end_date %q", got)
	}
	if !reflect.DeepEqual(plan.Milestones[0].DependsOn, []string{"milestone-0"}) {
		t.Fatalf("unexpected milestone depends_on %#v", plan.Milestones[0].DependsOn)
	}
	if plan.Milestones[0].Sequence != 2 {
		t.Fatalf("unexpected milestone sequence %d", plan.Milestones[0].Sequence)
	}
	if len(plan.Milestones[0].Tasks) != 1 {
		t.Fatalf("expected one task, got %#v", plan.Milestones[0].Tasks)
	}
	task := plan.Milestones[0].Tasks[0]
	if task.DueAt == nil || task.CompletedAt == nil {
		t.Fatalf("expected task dates, got %#v", task)
	}
	if got := task.DueAt.UTC().Format(time.RFC3339); got != dueAt {
		t.Fatalf("unexpected due_at %q", got)
	}
	if got := task.ScheduledAt.UTC().Format(time.RFC3339); got != scheduledAt {
		t.Fatalf("unexpected scheduled_at %q", got)
	}
	if got := task.StartAt.UTC().Format(time.RFC3339); got != startAt {
		t.Fatalf("unexpected start_at %q", got)
	}
	if got := task.TargetAt.UTC().Format(time.RFC3339); got != targetAt {
		t.Fatalf("unexpected target_at %q", got)
	}
	if got := task.EndAt.UTC().Format(time.RFC3339); got != endAt {
		t.Fatalf("unexpected end_at %q", got)
	}
	if !reflect.DeepEqual(task.DependsOn, []string{"task-0"}) {
		t.Fatalf("unexpected task depends_on %#v", task.DependsOn)
	}
	if task.Sequence != 3 {
		t.Fatalf("unexpected task sequence %d", task.Sequence)
	}
	if got := task.CompletedAt.UTC().Format(time.RFC3339); got != completedAt {
		t.Fatalf("unexpected completed_at %q", got)
	}
}

// ---------------------------------------------------------------------------
// PlanProgressUpdateTool
// ---------------------------------------------------------------------------

func TestPlanProgressUpdateTool_UpdatesSummaryAndStatus(t *testing.T) {
	richPlan := store.UserPlan{
		ID:     "p1",
		UserID: "u1",
		Title:  "Exit corp gig",
		Status: "active",
		Summary: "old summary",
		Milestones: []store.UserPlanMilestone{
			{ID: "m1", Title: "Ship v1", Status: "todo", Tasks: []store.UserPlanTask{
				{ID: "t1", Title: "Write tests", Status: "todo"},
			}},
		},
		TrackedMetrics:  []store.PlanTrackedMetric{{Name: "revenue"}},
		BaselineFacts:   []store.PlanFact{{Label: "start", Value: "$0"}},
		SuccessCriteria: []string{"$1k/day"},
		Cadence:         []store.PlanCadenceEntry{{Label: "mon", Activity: "standup"}},
	}
	fs := &fakePlanStore{plans: []store.UserPlan{richPlan}}
	tool := &PlanProgressUpdateTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	result, err := tool.Execute(ctx, map[string]any{
		"id":      "p1",
		"summary": "shipped the first feature",
		"status":  "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	saved := fs.upserts[0]
	if saved.Summary != "shipped the first feature" {
		t.Errorf("unexpected summary %q", saved.Summary)
	}
	// Structural fields must be preserved unchanged.
	if len(saved.Milestones) != 1 || saved.Milestones[0].ID != "m1" {
		t.Errorf("milestones were clobbered: %+v", saved.Milestones)
	}
	if len(saved.TrackedMetrics) != 1 || saved.TrackedMetrics[0].Name != "revenue" {
		t.Errorf("tracked_metrics were clobbered: %+v", saved.TrackedMetrics)
	}
	if len(saved.BaselineFacts) != 1 || saved.BaselineFacts[0].Label != "start" {
		t.Errorf("baseline_facts were clobbered: %+v", saved.BaselineFacts)
	}
	if len(saved.SuccessCriteria) != 1 || saved.SuccessCriteria[0] != "$1k/day" {
		t.Errorf("success_criteria were clobbered: %+v", saved.SuccessCriteria)
	}
	if len(saved.Cadence) != 1 || saved.Cadence[0].Activity != "standup" {
		t.Errorf("cadence was clobbered: %+v", saved.Cadence)
	}

	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if out["status"] != "ok" {
		t.Errorf("unexpected result status %q", out["status"])
	}
}

func TestPlanProgressUpdateTool_RejectsStructuralFields(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{ID: "p1", UserID: "u1", Title: "Plan"}}}
	tool := &PlanProgressUpdateTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	structuralCases := []string{
		"milestones", "steps", "objectives", "principles",
		"tracked_metrics", "baseline_facts", "success_criteria",
		"cadence", "supporting_sections", "metrics",
		"data_sources", "connectors", "category",
		"review_cadence", "target", "vision",
	}
	for _, field := range structuralCases {
		_, err := tool.Execute(ctx, map[string]any{
			"id":  "p1",
			field: "should be rejected",
		})
		if err == nil {
			t.Errorf("expected error for structural field %q, got nil", field)
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error for field %q does not mention the field: %v", field, err)
		}
	}
}

func TestPlanProgressUpdateTool_RequiresPlanID(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PlanProgressUpdateTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{"summary": "something"})
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("expected id required error, got %v", err)
	}
}

func TestPlanProgressUpdateTool_ReturnsNotFoundForUnknownPlan(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{}}
	tool := &PlanProgressUpdateTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{"id": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown plan, got nil")
	}
}

func TestPlanProgressUpdateTool_UpdatesTags(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{
		ID:     "p1",
		UserID: "u1",
		Title:  "Health goal",
		Tags:   []string{"fitness"},
	}}}
	tool := &PlanProgressUpdateTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"id":   "p1",
		"tags": []any{"fitness", "nutrition"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	got := fs.upserts[0].Tags
	if len(got) != 2 || got[0] != "fitness" || got[1] != "nutrition" {
		t.Errorf("unexpected tags %v", got)
	}
}

// ---------------------------------------------------------------------------
// plan_upsert merge-preservation regression tests
// ---------------------------------------------------------------------------

// richPlan is a fully populated plan used across merge-preservation tests.
var richPlan = store.UserPlan{
	ID:     "plan-rich",
	UserID: "u1",
	Title:  "Exit corp gig",
	Status: "active",
	Vision: "Operate a profitable independent studio",
	Target: "$1k/day",
	Summary: "Working toward financial independence.",
	Category: "work",
	ReviewCadence: "weekly",
	Tags:          []string{"career"},
	DataSources:   []string{"github"},
	Connectors:    []store.PlanConnector{{App: "github", Type: "personal_data_app"}},
	Metrics:       map[string]any{"target_clients": float64(3)},
	Milestones: []store.UserPlanMilestone{
		{ID: "m1", Title: "Acquire pipeline", Tasks: []store.UserPlanTask{
			{ID: "t1", Title: "Publish offer", Status: "done"},
			{ID: "t2", Title: "Close first client", Status: "todo"},
		}},
	},
	TrackedMetrics:  []store.PlanTrackedMetric{{Name: "monthly_revenue"}},
	BaselineFacts:   []store.PlanFact{{Label: "start_mrr", Value: "$0"}},
	SuccessCriteria: []string{"$1k/day sustained for 3 months"},
	Cadence: []store.PlanCadenceEntry{
		{Label: "mon", Activity: "weekly review"},
	},
	SupportingSections: []store.PlanSupportingSection{
		{Title: "Resources", Kind: "list"},
	},
	Objectives: []string{"Grow pipeline", "Ship product"},
	Principles: []string{"Ship small, learn fast"},
}

func TestPlanUpsertPartialUpdatePreservesStructuredFields(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{richPlan}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	// Only update title and status — every other field is absent from params.
	_, err := tool.Execute(ctx, map[string]any{
		"id":     "plan-rich",
		"title":  "Exit corp gig",
		"status": "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	saved := fs.upserts[0]

	// Structural fields must be preserved exactly as stored.
	if len(saved.Milestones) != 1 || saved.Milestones[0].ID != "m1" {
		t.Errorf("milestones clobbered: %+v", saved.Milestones)
	}
	if len(saved.Milestones[0].Tasks) != 2 {
		t.Errorf("tasks clobbered: %+v", saved.Milestones[0].Tasks)
	}
	if len(saved.TrackedMetrics) != 1 || saved.TrackedMetrics[0].Name != "monthly_revenue" {
		t.Errorf("tracked_metrics clobbered: %+v", saved.TrackedMetrics)
	}
	if len(saved.BaselineFacts) != 1 || saved.BaselineFacts[0].Label != "start_mrr" {
		t.Errorf("baseline_facts clobbered: %+v", saved.BaselineFacts)
	}
	if len(saved.SuccessCriteria) != 1 {
		t.Errorf("success_criteria clobbered: %+v", saved.SuccessCriteria)
	}
	if len(saved.Cadence) != 1 || saved.Cadence[0].Activity != "weekly review" {
		t.Errorf("cadence clobbered: %+v", saved.Cadence)
	}
	if len(saved.SupportingSections) != 1 {
		t.Errorf("supporting_sections clobbered: %+v", saved.SupportingSections)
	}
	if len(saved.Objectives) != 2 {
		t.Errorf("objectives clobbered: %+v", saved.Objectives)
	}
	if len(saved.Principles) != 1 {
		t.Errorf("principles clobbered: %+v", saved.Principles)
	}
	if v, _ := saved.Metrics["target_clients"].(float64); v != 3 {
		t.Errorf("metrics clobbered: %+v", saved.Metrics)
	}
	if saved.Vision != "Operate a profitable independent studio" {
		t.Errorf("vision clobbered: %q", saved.Vision)
	}
	if saved.Target != "$1k/day" {
		t.Errorf("target clobbered: %q", saved.Target)
	}
	if saved.Category != "work" {
		t.Errorf("category clobbered: %q", saved.Category)
	}
	if saved.ReviewCadence != "weekly" {
		t.Errorf("review_cadence clobbered: %q", saved.ReviewCadence)
	}
	if len(saved.DataSources) == 0 {
		t.Errorf("data_sources clobbered: %+v", saved.DataSources)
	}
	if len(saved.Connectors) == 0 {
		t.Errorf("connectors clobbered: %+v", saved.Connectors)
	}
}

func TestPlanUpsertPartialSummaryUpdatePreservesEverythingElse(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{richPlan}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"id":      "plan-rich",
		"title":   "Exit corp gig",
		"summary": "Updated progress note.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	saved := fs.upserts[0]

	if saved.Summary != "Updated progress note." {
		t.Errorf("summary not updated: %q", saved.Summary)
	}
	// Everything else untouched.
	if len(saved.Milestones) != 1 {
		t.Errorf("milestones clobbered: %+v", saved.Milestones)
	}
	if len(saved.TrackedMetrics) != 1 {
		t.Errorf("tracked_metrics clobbered: %+v", saved.TrackedMetrics)
	}
	if saved.Vision != "Operate a profitable independent studio" {
		t.Errorf("vision clobbered: %q", saved.Vision)
	}
}

func TestPlanUpsertExplicitMilestonesOverwriteStoredOnes(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{richPlan}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	// Explicit milestones in params must replace the stored ones.
	_, err := tool.Execute(ctx, map[string]any{
		"id":    "plan-rich",
		"title": "Exit corp gig",
		"milestones": []any{
			map[string]any{
				"id":    "m2",
				"title": "Land second client",
				"tasks": []any{
					map[string]any{"title": "Send proposal", "status": "todo"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	saved := fs.upserts[0]

	if len(saved.Milestones) != 1 || saved.Milestones[0].ID != "m2" {
		t.Errorf("expected explicit milestones, got: %+v", saved.Milestones)
	}
	// Non-milestone fields must still be preserved.
	if saved.Vision != "Operate a profitable independent studio" {
		t.Errorf("vision clobbered: %q", saved.Vision)
	}
	if len(saved.TrackedMetrics) != 1 {
		t.Errorf("tracked_metrics clobbered: %+v", saved.TrackedMetrics)
	}
}

func TestPlanUpsertNewPlanStartsFromZero(t *testing.T) {
	fs := &fakePlanStore{} // empty — no existing plans
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"title":   "Brand new plan",
		"summary": "Starting fresh.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	saved := fs.upserts[0]

	if saved.Title != "Brand new plan" {
		t.Errorf("unexpected title: %q", saved.Title)
	}
	if saved.Summary != "Starting fresh." {
		t.Errorf("unexpected summary: %q", saved.Summary)
	}
	if len(saved.Milestones) != 0 {
		t.Errorf("expected no milestones on new plan, got %+v", saved.Milestones)
	}
	if saved.Vision != "" {
		t.Errorf("expected empty vision on new plan, got %q", saved.Vision)
	}
}

func TestPlanUpsertPreservesStatusWhenNotInParams(t *testing.T) {
	existing := store.UserPlan{
		ID:     "plan-paused",
		UserID: "u1",
		Title:  "Paused project",
		Status: "paused",
	}
	fs := &fakePlanStore{plans: []store.UserPlan{existing}}
	tool := &PlanUpsertTool{Store: fs}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"id":      "plan-paused",
		"title":   "Paused project",
		"summary": "Still paused.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fs.upserts[0].Status != "paused" {
		t.Errorf("status clobbered to %q; expected paused", fs.upserts[0].Status)
	}
}
