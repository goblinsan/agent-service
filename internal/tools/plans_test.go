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

func TestPlanListReturnsPlansForUser(t *testing.T) {
	fs := &fakePlanStore{plans: []store.UserPlan{{ID: "p1", UserID: "u1", Title: "Exit corp", Status: "active"}}}
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
	if got := len(m["plans"].([]map[string]any)); got != 1 {
		t.Errorf("expected 1 plan, got %d", got)
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
}
