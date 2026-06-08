package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
)

// PlanListTool returns the caller's active plans/goals.
type PlanListTool struct {
	Store    store.Store
	Location string
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
	localNow := time.Now().UTC().In(resolveLocation(t.Location))
	for _, p := range plans {
		out = append(out, summarizePlanForTool(p, localNow))
	}
	return map[string]any{
		"user_id":       uid,
		"local_date":    localNow.Format("2006-01-02"),
		"local_weekday": localNow.Weekday().String(),
		"plans":         out,
	}, nil
}

func summarizePlanForTool(p store.UserPlan, localNow time.Time) map[string]any {
	const maxMilestones = 4
	const maxTasksPerMilestone = 3
	const maxSteps = 6

	milestones := make([]map[string]any, 0, minInt(len(p.Milestones), maxMilestones))
	for i, milestone := range p.Milestones {
		if i >= maxMilestones {
			break
		}
		entry := map[string]any{
			"id":             milestone.ID,
			"title":          milestone.Title,
			"status":         milestone.Status,
			"summary":        milestone.Summary,
			"scheduled_date": milestone.ScheduledDate,
			"start_date":     milestone.StartDate,
			"target_date":    milestone.TargetDate,
			"end_date":       milestone.EndDate,
			"depends_on":     milestone.DependsOn,
			"sequence":       milestone.Sequence,
		}
		taskCount := len(milestone.Tasks)
		if taskCount > 0 && taskCount <= 12 {
			tasks := make([]map[string]any, 0, minInt(taskCount, maxTasksPerMilestone))
			for j, task := range milestone.Tasks {
				if j >= maxTasksPerMilestone {
					break
				}
				tasks = append(tasks, map[string]any{
					"id":           task.ID,
					"title":        task.Title,
					"status":       task.Status,
					"notes":        task.Notes,
					"scheduled_at": task.ScheduledAt,
					"start_at":     task.StartAt,
					"target_at":    task.TargetAt,
					"due_at":       task.DueAt,
					"end_at":       task.EndAt,
					"completed_at": task.CompletedAt,
					"depends_on":   task.DependsOn,
					"sequence":     task.Sequence,
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
		"id":              p.ID,
		"title":           p.Title,
		"status":          p.Status,
		"vision":          p.Vision,
		"target":          p.Target,
		"category":        p.Category,
		"tags":            p.Tags,
		"data_sources":    p.DataSources,
		"connectors":      p.Connectors,
		"review_cadence":  p.ReviewCadence,
		"summary":         p.Summary,
		"metrics":         p.Metrics,
		"progress":        p.Progress,
		"today_actions":   store.PlanTodayActions(p, localNow),
		"next_open_tasks": store.PlanNextOpenTasks(p, 3),
		"updated_at":      p.UpdatedAt,
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

func resolveLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		name = "UTC"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
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
			{Name: "milestones", Type: "array", Description: "Optional ordered milestone objects. Milestones support explicit scheduling fields: scheduled_date/start_date/target_date/end_date (RFC3339), optional depends_on ids, optional sequence, and tasks. Tasks support scheduled_at/start_at/target_at/due_at/end_at/completed_at (RFC3339), optional depends_on ids, and optional sequence.", Required: false},
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
	rawID, _ := params["id"].(string)
	id, created, err := resolvePlanIdentity(ctx, t.Store, uid, rawID, title)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = newPlanID()
	}

	// Fetch the existing plan when updating so that omitted structured fields
	// preserve their stored values rather than being erased by the partial payload.
	// Only fields explicitly present in params overwrite existing data.
	base := &store.UserPlan{ID: id, UserID: uid}
	if !created {
		if fetched, fetchErr := t.Store.GetUserPlan(ctx, uid, id); fetchErr == nil {
			base = fetched
		}
	}

	status, _ := params["status"].(string)
	status = strings.TrimSpace(status)
	if status == "" {
		if created || base.Status == "" {
			status = "active"
		} else {
			status = base.Status
		}
	}
	// title and status are always applied (title is required; status always meaningful).
	base.Title = title
	base.Status = status

	// For every optional field: only overwrite when the key appears in params.
	if _, ok := params["summary"]; ok {
		v, _ := params["summary"].(string)
		base.Summary = strings.TrimSpace(v)
	}
	if _, ok := params["vision"]; ok {
		v, _ := params["vision"].(string)
		base.Vision = strings.TrimSpace(v)
	}
	if _, ok := params["target"]; ok {
		v, _ := params["target"].(string)
		base.Target = strings.TrimSpace(v)
	}
	if _, ok := params["category"]; ok {
		v, _ := params["category"].(string)
		base.Category = strings.TrimSpace(v)
	}
	if _, ok := params["review_cadence"]; ok {
		v, _ := params["review_cadence"].(string)
		base.ReviewCadence = strings.TrimSpace(v)
	}
	if _, ok := params["tags"]; ok {
		base.Tags, err = normalizeStringList(params["tags"], "tags")
		if err != nil {
			return nil, err
		}
	}
	if _, ok := params["data_sources"]; ok {
		base.DataSources, err = normalizeStringList(params["data_sources"], "data_sources")
		if err != nil {
			return nil, err
		}
	}
	if _, ok := params["connectors"]; ok {
		base.Connectors, err = normalizePlanConnectors(params["connectors"])
		if err != nil {
			return nil, err
		}
	}
	base.DataSources = mergeDataSourcesWithConnectors(base.DataSources, base.Connectors)
	if _, ok := params["metrics"]; ok {
		base.Metrics, err = normalizeObject(params["metrics"], "metrics")
		if err != nil {
			return nil, err
		}
	}
	if _, ok := params["milestones"]; ok {
		base.Milestones, err = normalizePlanMilestones(params["milestones"])
		if err != nil {
			return nil, err
		}
	}
	if _, ok := params["steps"]; ok {
		base.Steps, err = normalizePlanSteps(params["steps"])
		if err != nil {
			return nil, err
		}
	}

	plan := base
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
		if scheduledDate, ok := parseOptionalRFC3339(asString(obj["scheduled_date"])); ok {
			m.ScheduledDate = &scheduledDate
		}
		if startDate, ok := parseOptionalRFC3339(asString(obj["start_date"])); ok {
			m.StartDate = &startDate
		}
		if endDate, ok := parseOptionalRFC3339(asString(obj["end_date"])); ok {
			m.EndDate = &endDate
		}
		if dependsOn, err := normalizeIDList(obj["depends_on"], fmt.Sprintf("milestones[%d].depends_on", i)); err != nil {
			return nil, err
		} else {
			m.DependsOn = dependsOn
		}
		if sequence, ok, err := parseOptionalInt(obj["sequence"], fmt.Sprintf("milestones[%d].sequence", i)); err != nil {
			return nil, err
		} else if ok {
			m.Sequence = sequence
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
		if scheduledAt, ok := parseOptionalRFC3339(asString(obj["scheduled_at"])); ok {
			task.ScheduledAt = &scheduledAt
		}
		if startAt, ok := parseOptionalRFC3339(asString(obj["start_at"])); ok {
			task.StartAt = &startAt
		}
		if targetAt, ok := parseOptionalRFC3339(asString(obj["target_at"])); ok {
			task.TargetAt = &targetAt
		}
		if endAt, ok := parseOptionalRFC3339(asString(obj["end_at"])); ok {
			task.EndAt = &endAt
		}
		if completedAt, ok := parseOptionalRFC3339(asString(obj["completed_at"])); ok {
			task.CompletedAt = &completedAt
		}
		if dependsOn, err := normalizeIDList(obj["depends_on"], fmt.Sprintf("milestones[%d].tasks[%d].depends_on", milestoneIndex, i)); err != nil {
			return nil, err
		} else {
			task.DependsOn = dependsOn
		}
		if sequence, ok, err := parseOptionalInt(obj["sequence"], fmt.Sprintf("milestones[%d].tasks[%d].sequence", milestoneIndex, i)); err != nil {
			return nil, err
		} else if ok {
			task.Sequence = sequence
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

func normalizeIDList(v any, field string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	values, err := normalizeStringList(v, field)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func parseOptionalInt(v any, field string) (int, bool, error) {
	if v == nil {
		return 0, false, nil
	}
	switch typed := v.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, false, fmt.Errorf("%s must be an integer", field)
		}
		return int(typed), true, nil
	case int:
		return typed, true, nil
	case int32:
		return int(typed), true, nil
	case int64:
		return int(typed), true, nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false, fmt.Errorf("%s must be an integer", field)
		}
		return int(parsed), true, nil
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return 0, false, nil
		}
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false, fmt.Errorf("%s must be an integer", field)
		}
		return parsed, true, nil
	default:
		return 0, false, fmt.Errorf("%s must be an integer", field)
	}
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

// planProgressUpdateStructuralFields is the exhaustive set of fields that
// plan_progress_update must never write, even if a caller supplies them.
var planProgressUpdateStructuralFields = []string{
	"milestones", "steps", "objectives", "principles",
	"tracked_metrics", "baseline_facts", "success_criteria",
	"cadence", "supporting_sections", "metrics",
	"data_sources", "connectors", "category",
	"review_cadence", "target", "vision",
}

// PlanProgressUpdateTool writes lightweight progress metadata to an existing
// plan without touching structural fields. Scheduled check-in runs must use
// this instead of plan_upsert so that milestones, tasks, metrics, and other
// rich plan data are never overwritten by a partial payload.
type PlanProgressUpdateTool struct {
	Store store.Store
}

func (t *PlanProgressUpdateTool) Definition() Tool {
	return Tool{
		Name: "plan_progress_update",
		Description: "Update lightweight progress metadata (summary, status, tags) on an existing plan. " +
			"Safe for scheduled check-ins: never modifies milestones, tasks, metrics, success criteria, cadence, or other structural fields. " +
			"Use plan_upsert for full plan editing in explicit user-driven flows.",
		Params: []Param{
			{Name: "id", Type: "string", Description: "ID of the plan to update (from plan_list).", Required: true},
			{Name: "summary", Type: "string", Description: "Optional updated one- or two-sentence progress summary.", Required: false},
			{Name: "status", Type: "string", Description: "Optional updated status: draft, active, paused, done, abandoned.", Required: false},
			{Name: "tags", Type: "array", Description: "Optional updated list of free-form tags.", Required: false},
		},
	}
}

func (t *PlanProgressUpdateTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("plan store not configured")
	}
	for _, field := range planProgressUpdateStructuralFields {
		if _, ok := params[field]; ok {
			return nil, fmt.Errorf("plan_progress_update cannot modify %q; use plan_upsert for full plan editing", field)
		}
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot update plan")
	}
	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("id is required")
	}
	existing, err := t.Store.GetUserPlan(ctx, uid, id)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	if summary, ok := params["summary"].(string); ok {
		existing.Summary = strings.TrimSpace(summary)
	}
	if rawStatus, ok := params["status"].(string); ok {
		if s := strings.TrimSpace(rawStatus); s != "" {
			existing.Status = s
		}
	}
	if tagsParam, ok := params["tags"]; ok && tagsParam != nil {
		tags, err := normalizeStringList(tagsParam, "tags")
		if err != nil {
			return nil, err
		}
		existing.Tags = tags
	}
	store.NormalizeUserPlan(existing)
	if err := t.Store.UpsertUserPlan(ctx, existing); err != nil {
		return nil, fmt.Errorf("update plan: %w", err)
	}
	return map[string]any{
		"status": "ok",
		"plan": map[string]any{
			"id":      existing.ID,
			"title":   existing.Title,
			"status":  existing.Status,
			"summary": existing.Summary,
			"tags":    existing.Tags,
		},
	}, nil
}
