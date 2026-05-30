package tools

import (
	"context"
	"testing"

	"github.com/goblinsan/agent-service/internal/store"
)

func TestHealthFitIngestSummaryCreatesPlanAndEvent(t *testing.T) {
	fs := &fakePlanStore{}
	tool := &PersonalDataIngestTool{
		Store:         fs,
		App:           "healthfit",
		Domain:        "health",
		ToolName:      "healthfit_ingest_summary",
		ToolDesc:      "test",
		PlanTitle:     "Health goals & activity",
		DefaultSource: "HealthFit",
		EventKind:     "health_sync",
	}
	ctx := WithUserID(context.Background(), "u1")

	res, err := tool.Execute(ctx, map[string]any{
		"summary": "HealthFit sync complete",
		"metrics": map[string]any{
			"steps":          10000,
			"active_minutes": 62,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["created"] != true {
		t.Fatalf("expected created=true, got %v", res.(map[string]any)["created"])
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	plan := fs.upserts[0]
	if plan.Category != "health" {
		t.Fatalf("expected health category, got %q", plan.Category)
	}
	if len(plan.Connectors) != 1 || plan.Connectors[0].App != "healthfit" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if len(plan.DataSources) == 0 || plan.DataSources[0] != "healthfit" {
		t.Fatalf("expected healthfit data source, got %#v", plan.DataSources)
	}
	if got := plan.Metrics["steps"]; got != 10000 {
		t.Fatalf("unexpected steps metric %#v", plan.Metrics)
	}
	if len(fs.events) != 1 {
		t.Fatalf("expected 1 user event, got %d", len(fs.events))
	}
	if fs.events[0].Kind != "health_sync" {
		t.Fatalf("unexpected event kind %q", fs.events[0].Kind)
	}
}

func TestLoseItIngestSummaryUpdatesExistingPlan(t *testing.T) {
	fs := &fakePlanStore{
		plans: []store.UserPlan{
			{
				ID:       "plan-nutrition-1",
				UserID:   "u1",
				Title:    "Nutrition goals",
				Status:   "active",
				Category: "nutrition",
				Metrics:  map[string]any{"calories_consumed": 1800.0},
			},
		},
	}
	tool := &PersonalDataIngestTool{
		Store:         fs,
		App:           "loseit",
		Domain:        "nutrition",
		ToolName:      "loseit_ingest_summary",
		ToolDesc:      "test",
		PlanTitle:     "Nutrition goals & intake",
		DefaultSource: "Lose It",
		EventKind:     "nutrition_sync",
	}
	ctx := WithUserID(context.Background(), "u1")

	res, err := tool.Execute(ctx, map[string]any{
		"metrics": map[string]any{
			"calories_consumed": 1650,
			"protein_grams":     145,
		},
		"external_id": "loseit-account-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["created"] != false {
		t.Fatalf("expected created=false, got %v", res.(map[string]any)["created"])
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	plan := fs.upserts[0]
	if plan.ID != "plan-nutrition-1" {
		t.Fatalf("expected existing plan id, got %q", plan.ID)
	}
	if len(plan.Connectors) != 1 || plan.Connectors[0].ExternalID != "loseit-account-7" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if plan.Metrics["calories_consumed"] != 1650 {
		t.Fatalf("expected updated calories metric, got %#v", plan.Metrics["calories_consumed"])
	}
	if len(fs.events) != 1 || fs.events[0].Kind != "nutrition_sync" {
		t.Fatalf("unexpected events %#v", fs.events)
	}
}

func TestAppleHealthActivityAndNutritionStaySeparate(t *testing.T) {
	fs := &fakePlanStore{
		plans: []store.UserPlan{
			{
				ID:       "plan-health-1",
				UserID:   "u1",
				Title:    "Health goals",
				Status:   "active",
				Category: "health",
				Connectors: []store.PlanConnector{{
					App:    "apple-health",
					Type:   "personal_data_app",
					Domain: "health",
				}},
			},
			{
				ID:       "plan-nutrition-1",
				UserID:   "u1",
				Title:    "Nutrition goals",
				Status:   "active",
				Category: "nutrition",
			},
		},
	}
	tool := &PersonalDataIngestTool{
		Store:         fs,
		App:           "apple-health",
		Domain:        "nutrition",
		ToolName:      "apple_health_ingest_nutrition",
		ToolDesc:      "test",
		PlanTitle:     "Nutrition goals & intake",
		DefaultSource: "Apple Health",
		EventKind:     "nutrition_sync",
	}
	ctx := WithUserID(context.Background(), "u1")

	_, err := tool.Execute(ctx, map[string]any{
		"metrics": map[string]any{
			"dietary_energy_kcal": 1900,
			"protein_grams":       138,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upserts))
	}
	plan := fs.upserts[0]
	if plan.ID != "plan-nutrition-1" {
		t.Fatalf("expected nutrition plan, got %q", plan.ID)
	}
	if len(plan.Connectors) != 1 || plan.Connectors[0].App != "apple-health" || plan.Connectors[0].Domain != "nutrition" {
		t.Fatalf("unexpected connectors %#v", plan.Connectors)
	}
	if plan.Metrics["protein_grams"] != 138 {
		t.Fatalf("expected nutrition metrics on nutrition plan, got %#v", plan.Metrics)
	}
}
