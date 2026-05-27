package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

// PlanListTool returns the caller's active plans/goals.
type PlanListTool struct {
	Store store.Store
}

func (t *PlanListTool) Definition() Tool {
	return Tool{
		Name:        "plan_list",
		Description: "Returns the current user's active goals and plans (status not 'done' or 'abandoned'). Each plan has an id, title, status, optional vision/target, milestones with tasks, roll-up progress, and compatibility fields like summary/metrics/steps. Use this before creating a new plan to avoid duplicates and to recall the id you need for plan_upsert.",
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
		out = append(out, summarizePlanForTool(p))
	}
	return map[string]any{"user_id": uid, "plans": out}, nil
}

func summarizePlanForTool(p store.UserPlan) map[string]any {
	const maxMilestones = 4
	const maxTasksPerMilestone = 3
	const maxSteps = 6

	milestones := make([]map[string]any, 0, minInt(len(p.Milestones), maxMilestones))
	for i, milestone := range p.Milestones {
		if i >= maxMilestones {
			break
		}
		entry := map[string]any{
			"id":      milestone.ID,
			"title":   milestone.Title,
			"status":  milestone.Status,
			"summary": milestone.Summary,
		}
		taskCount := len(milestone.Tasks)
		if taskCount > 0 && taskCount <= 12 {
			tasks := make([]map[string]any, 0, minInt(taskCount, maxTasksPerMilestone))
			for j, task := range milestone.Tasks {
				if j >= maxTasksPerMilestone {
					break
				}
				tasks = append(tasks, map[string]any{
					"id":     task.ID,
					"title":  task.Title,
					"status": task.Status,
					"notes":  task.Notes,
				})
			}
			entry["tasks"] = tasks
			if taskCount > maxTasksPerMilestone {
				entry["more_tasks"] = taskCount - maxTasksPerMilestone
			}
		} else if taskCount > 0 {
			entry["task_count"] = taskCount
		}
		milestones = append(milestones, entry)
	}

	steps := make([]map[string]any, 0, minInt(len(p.Steps), maxSteps))
	if len(p.Milestones) == 0 && len(p.Steps) <= 12 {
		for i, step := range p.Steps {
			if i >= maxSteps {
				break
			}
			steps = append(steps, step)
		}
	}

	out := map[string]any{
		"id":             p.ID,
		"title":          p.Title,
		"status":         p.Status,
		"vision":         p.Vision,
		"target":         p.Target,
		"category":       p.Category,
		"tags":           p.Tags,
		"data_sources":   p.DataSources,
		"connectors":     p.Connectors,
		"review_cadence": p.ReviewCadence,
		"summary":        p.Summary,
		"metrics":        p.Metrics,
		"progress":       p.Progress,
		"updated_at":     p.UpdatedAt,
	}
	if len(milestones) > 0 {
		out["milestones"] = milestones
		if len(p.Milestones) > maxMilestones {
			out["more_milestones"] = len(p.Milestones) - maxMilestones
		}
	}
	if len(steps) > 0 {
		out["steps"] = steps
		if len(p.Steps) > maxSteps {
			out["more_steps"] = len(p.Steps) - maxSteps
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// PlanUpsertTool creates or updates a goal/plan for the current user.
type PlanUpsertTool struct {
	Store store.Store
}

func (t *PlanUpsertTool) Definition() Tool {
	return Tool{
		Name:        "plan_upsert",
		Description: "Create or update a durable goal/plan for the current user. Omit 'id' to create a new plan; pass an existing id (from plan_list) to update one. Supports a top-level vision/target, milestones, milestone tasks, and computed roll-up progress.",
		Params: []Param{
			{Name: "id", Type: "string", Description: "Existing plan id from plan_list. Omit to create a new plan.", Required: false},
			{Name: "title", Type: "string", Description: "Short title for the goal/plan (e.g. 'Exit corp gig — $1k/day').", Required: true},
			{Name: "status", Type: "string", Description: "One of: draft, active, paused, done, abandoned. Defaults to 'active' on create.", Required: false},
			{Name: "vision", Type: "string", Description: "Optional long-horizon vision statement that explains the desired future state.", Required: false},
			{Name: "target", Type: "string", Description: "Optional concrete target outcome, KPI, or deadline objective.", Required: false},
			{Name: "category", Type: "string", Description: "Optional plan category such as work, health, finance, or social.", Required: false},
			{Name: "tags", Type: "array", Description: "Optional list of free-form tags for filtering and grouping.", Required: false},
			{Name: "data_sources", Type: "array", Description: "Optional list of systems or apps this plan depends on, for example apple-health, strava, loseit, budget-sheet, or github.", Required: false},
			{Name: "connectors", Type: "array", Description: "Optional list of connector objects for integrated apps. Each object should include app (required) plus optional type, domain, and external_id.", Required: false},
			{Name: "review_cadence", Type: "string", Description: "Optional human-readable review cadence such as daily, weekday-morning, weekly, or quarterly.", Required: false},
			{Name: "summary", Type: "string", Description: "One- or two-sentence description of the goal and why it matters.", Required: false},
			{Name: "metrics", Type: "object", Description: "Optional structured metrics map, for example {\"target_workouts_per_week\":4,\"weekly_budget_usd\":500}.", Required: false},
			{Name: "milestones", Type: "array", Description: "Optional ordered milestone objects. Each milestone is {\"id\":\"...\",\"title\":\"...\",\"status\":\"todo|doing|done|blocked\",\"summary\":\"...\",\"target_date\":\"RFC3339(optional)\",\"tasks\":[{\"id\":\"...\",\"title\":\"...\",\"status\":\"todo|doing|done|blocked\",\"notes\":\"...\",\"due_at\":\"RFC3339(optional)\"}]}.", Required: false},
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
	vision, _ := params["vision"].(string)
	vision = strings.TrimSpace(vision)
	target, _ := params["target"].(string)
	target = strings.TrimSpace(target)
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
	connectors, err := normalizePlanConnectors(params["connectors"])
	if err != nil {
		return nil, err
	}
	dataSources = mergeDataSourcesWithConnectors(dataSources, connectors)
	reviewCadence, _ := params["review_cadence"].(string)
	reviewCadence = strings.TrimSpace(reviewCadence)
	metrics, err := normalizeObject(params["metrics"], "metrics")
	if err != nil {
		return nil, err
	}
	milestones, err := normalizePlanMilestones(params["milestones"])
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
		Vision:        vision,
		Target:        target,
		Category:      category,
		Tags:          tags,
		DataSources:   dataSources,
		Connectors:    connectors,
		ReviewCadence: reviewCadence,
		Summary:       summary,
		Metrics:       metrics,
		Milestones:    milestones,
		Steps:         steps,
	}
	store.NormalizeUserPlan(plan)
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
			"vision":         plan.Vision,
			"target":         plan.Target,
			"category":       plan.Category,
			"tags":           plan.Tags,
			"data_sources":   plan.DataSources,
			"connectors":     plan.Connectors,
			"review_cadence": plan.ReviewCadence,
			"summary":        plan.Summary,
			"metrics":        plan.Metrics,
			"milestones":     plan.Milestones,
			"progress":       plan.Progress,
			"steps":          plan.Steps,
		},
	}, nil
}

func normalizeStringList(v any, field string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	if typed, ok := v.([]string); ok {
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value := strings.TrimSpace(item)
			if value == "" {
				continue
			}
			out = append(out, value)
		}
		return out, nil
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

func normalizePlanConnectors(v any) ([]store.PlanConnector, error) {
	if v == nil {
		return nil, nil
	}
	if typed, ok := v.([]store.PlanConnector); ok {
		out := make([]store.PlanConnector, 0, len(typed))
		for i, item := range typed {
			app := strings.TrimSpace(item.App)
			if app == "" {
				return nil, fmt.Errorf("connectors[%d].app is required", i)
			}
			typ := strings.TrimSpace(item.Type)
			if typ == "" {
				typ = "personal_data_app"
			}
			out = append(out, store.PlanConnector{
				App:        app,
				Type:       typ,
				Domain:     strings.TrimSpace(item.Domain),
				ExternalID: strings.TrimSpace(item.ExternalID),
			})
		}
		return out, nil
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, fmt.Errorf("connectors string is not valid JSON: %w", err)
		}
		v = decoded
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.New("connectors must be an array of objects")
	}
	out := make([]store.PlanConnector, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("connectors[%d] must be an object", i)
		}
		app, _ := obj["app"].(string)
		app = strings.TrimSpace(app)
		if app == "" {
			return nil, fmt.Errorf("connectors[%d].app is required", i)
		}
		typ, _ := obj["type"].(string)
		typ = strings.TrimSpace(typ)
		if typ == "" {
			typ = "personal_data_app"
		}
		domain, _ := obj["domain"].(string)
		externalID, _ := obj["external_id"].(string)
		out = append(out, store.PlanConnector{
			App:        app,
			Type:       typ,
			Domain:     strings.TrimSpace(domain),
			ExternalID: strings.TrimSpace(externalID),
		})
	}
	return out, nil
}

func mergeDataSourcesWithConnectors(dataSources []string, connectors []store.PlanConnector) []string {
	out := append([]string(nil), dataSources...)
	for _, c := range connectors {
		if strings.TrimSpace(c.App) == "" {
			continue
		}
		if !containsString(out, c.App) {
			out = append(out, c.App)
		}
	}
	return out
}

func normalizePlanMilestones(v any) ([]store.UserPlanMilestone, error) {
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
			return nil, fmt.Errorf("milestones string is not valid JSON: %w", err)
		}
		v = decoded
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.New("milestones must be an array of objects")
	}
	out := make([]store.UserPlanMilestone, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("milestones[%d] must be an object", i)
		}
		title, _ := obj["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("milestones[%d].title is required", i)
		}
		m := store.UserPlanMilestone{
			Title:   title,
			Status:  normalizePlanState(asString(obj["status"]), "todo"),
			Summary: strings.TrimSpace(asString(obj["summary"])),
			ID:      strings.TrimSpace(asString(obj["id"])),
		}
		if targetDate, ok := parseOptionalRFC3339(asString(obj["target_date"])); ok {
			m.TargetDate = &targetDate
		}
		tasks, err := normalizePlanTasks(obj["tasks"], i)
		if err != nil {
			return nil, err
		}
		m.Tasks = tasks
		out = append(out, m)
	}
	return out, nil
}

func normalizePlanTasks(v any, milestoneIndex int) ([]store.UserPlanTask, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("milestones[%d].tasks must be an array", milestoneIndex)
	}
	out := make([]store.UserPlanTask, 0, len(raw))
	for i, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("milestones[%d].tasks[%d] must be an object", milestoneIndex, i)
		}
		title := strings.TrimSpace(asString(obj["title"]))
		if title == "" {
			return nil, fmt.Errorf("milestones[%d].tasks[%d].title is required", milestoneIndex, i)
		}
		task := store.UserPlanTask{
			ID:     strings.TrimSpace(asString(obj["id"])),
			Title:  title,
			Status: normalizePlanState(asString(obj["status"]), "todo"),
			Notes:  strings.TrimSpace(asString(obj["notes"])),
		}
		if dueAt, ok := parseOptionalRFC3339(asString(obj["due_at"])); ok {
			task.DueAt = &dueAt
		}
		if completedAt, ok := parseOptionalRFC3339(asString(obj["completed_at"])); ok {
			task.CompletedAt = &completedAt
		}
		out = append(out, task)
	}
	return out, nil
}

func parseOptionalRFC3339(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func normalizePlanState(status, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "todo", "doing", "done", "blocked", "paused", "active":
		return normalized
	default:
		return fallback
	}
}

func newPlanID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "plan-fallback"
	}
	return "plan-" + hex.EncodeToString(b[:])
}
