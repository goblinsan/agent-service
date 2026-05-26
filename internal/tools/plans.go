package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

// PlanListTool returns the caller's active plans/goals.
type PlanListTool struct {
	Store store.Store
}

func (t *PlanListTool) Definition() Tool {
	return Tool{
		Name:        "plan_list",
		Description: "Returns the current user's active goals and plans (status not 'done' or 'abandoned'). Each plan has an id, title, status, optional category/tags/data sources/review cadence, summary, metrics, and ordered steps. Use this before creating a new plan to avoid duplicates and to recall the id you need for plan_upsert.",
	}
}

func (t *PlanListTool) Execute(ctx context.Context, _ map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("plan store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return map[string]any{"plans": []any{}, "note": "no authenticated user on this run"}, nil
	}
	plans, err := t.Store.ListActivePlans(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	out := make([]map[string]any, 0, len(plans))
	for _, p := range plans {
		out = append(out, map[string]any{
			"id":             p.ID,
			"title":          p.Title,
			"status":         p.Status,
			"category":       p.Category,
			"tags":           p.Tags,
			"data_sources":   p.DataSources,
			"review_cadence": p.ReviewCadence,
			"summary":        p.Summary,
			"metrics":        p.Metrics,
			"steps":          p.Steps,
			"updated_at":     p.UpdatedAt,
		})
	}
	return map[string]any{"user_id": uid, "plans": out}, nil
}

// PlanUpsertTool creates or updates a goal/plan for the current user.
type PlanUpsertTool struct {
	Store store.Store
}

func (t *PlanUpsertTool) Definition() Tool {
	return Tool{
		Name:        "plan_upsert",
		Description: "Create or update a durable goal/plan for the current user. Omit 'id' to create a new plan; pass an existing id (from plan_list) to update one. Use this when the user states a goal, agrees to a multi-step commitment, or asks you to track progress. Mark a plan 'done' or 'abandoned' to remove it from the active list.",
		Params: []Param{
			{Name: "id", Type: "string", Description: "Existing plan id from plan_list. Omit to create a new plan.", Required: false},
			{Name: "title", Type: "string", Description: "Short title for the goal/plan (e.g. 'Exit corp gig — $1k/day').", Required: true},
			{Name: "status", Type: "string", Description: "One of: draft, active, paused, done, abandoned. Defaults to 'active' on create.", Required: false},
			{Name: "category", Type: "string", Description: "Optional plan category such as work, health, finance, or social.", Required: false},
			{Name: "tags", Type: "array", Description: "Optional list of free-form tags for filtering and grouping.", Required: false},
			{Name: "data_sources", Type: "array", Description: "Optional list of systems or apps this plan depends on, for example apple-health, strava, loseit, budget-sheet, or github.", Required: false},
			{Name: "review_cadence", Type: "string", Description: "Optional human-readable review cadence such as daily, weekday-morning, weekly, or quarterly.", Required: false},
			{Name: "summary", Type: "string", Description: "One- or two-sentence description of the goal and why it matters.", Required: false},
			{Name: "metrics", Type: "object", Description: "Optional structured metrics map, for example {\"target_workouts_per_week\":4,\"weekly_budget_usd\":500}.", Required: false},
			{Name: "steps", Type: "array", Description: "Ordered list of step objects. Each step is an object like {\"title\":\"...\",\"status\":\"todo|doing|done\",\"notes\":\"...\"}. Pass the full revised list when updating.", Required: false},
		},
	}
}

func (t *PlanUpsertTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("plan store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot write plan")
	}
	title, _ := params["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	created := false
	if id == "" {
		id = newPlanID()
		created = true
	}
	status, _ := params["status"].(string)
	status = strings.TrimSpace(status)
	if status == "" {
		if created {
			status = "active"
		} else {
			status = "active"
		}
	}
	summary, _ := params["summary"].(string)
	category, _ := params["category"].(string)
	category = strings.TrimSpace(category)
	tags, err := normalizeStringList(params["tags"], "tags")
	if err != nil {
		return nil, err
	}
	dataSources, err := normalizeStringList(params["data_sources"], "data_sources")
	if err != nil {
		return nil, err
	}
	reviewCadence, _ := params["review_cadence"].(string)
	reviewCadence = strings.TrimSpace(reviewCadence)
	metrics, err := normalizeObject(params["metrics"], "metrics")
	if err != nil {
		return nil, err
	}

	steps, err := normalizePlanSteps(params["steps"])
	if err != nil {
		return nil, err
	}

	plan := &store.UserPlan{
		ID:            id,
		UserID:        uid,
		Title:         title,
		Status:        status,
		Category:      category,
		Tags:          tags,
		DataSources:   dataSources,
		ReviewCadence: reviewCadence,
		Summary:       summary,
		Metrics:       metrics,
		Steps:         steps,
	}
	if err := t.Store.UpsertUserPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("upsert plan: %w", err)
	}
	return map[string]any{
		"status":  "ok",
		"created": created,
		"plan": map[string]any{
			"id":             plan.ID,
			"title":          plan.Title,
			"status":         plan.Status,
			"category":       plan.Category,
			"tags":           plan.Tags,
			"data_sources":   plan.DataSources,
			"review_cadence": plan.ReviewCadence,
			"summary":        plan.Summary,
			"metrics":        plan.Metrics,
			"steps":          plan.Steps,
		},
	}, nil
}

func normalizeStringList(v any, field string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("%s string is not valid JSON: %w", field, err)
		}
		v = decoded
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", field)
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", field, i)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out, nil
}

func normalizeObject(v any, field string) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("%s string is not valid JSON: %w", field, err)
		}
		v = decoded
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	return obj, nil
}

func normalizePlanSteps(v any) ([]map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	// Some models pass arrays as JSON-encoded strings. Decode first.
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("steps string is not valid JSON: %w", err)
		}
		v = decoded
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.New("steps must be an array of objects")
	}
	out := make([]map[string]any, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("steps[%d] must be an object", i)
		}
		out = append(out, obj)
	}
	return out, nil
}

func newPlanID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "plan-fallback"
	}
	return "plan-" + hex.EncodeToString(b[:])
}
