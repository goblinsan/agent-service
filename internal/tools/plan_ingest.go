package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

var (
	checkboxStepPattern = regexp.MustCompile(`^\s*[-*]\s*\[(?P<mark>[ xX])\]\s*(?P<body>.+)$`)
	bulletStepPattern   = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(?P<body>.+)$`)
	headingPattern      = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+)$`)
)

// PlanIngestTextTool converts pasted plain-text or markdown plan documents into
// durable user_plans rows so other agents/systems can seed upcoming work.
type PlanIngestTextTool struct {
	Store store.Store
}

func (t *PlanIngestTextTool) Definition() Tool {
	return Tool{
		Name:        "plan_ingest_text",
		Description: "Parse a pasted plain-text or markdown project/plan document and store it as a durable user plan. Use this when the user pastes a plan from another system and wants it tracked for future check-ins or execution. Pass an existing id to refresh a known plan instead of creating a duplicate.",
		Params: []Param{
			{Name: "text", Type: "string", Description: "Raw plain-text or markdown plan content to ingest.", Required: true},
			{Name: "title", Type: "string", Description: "Optional explicit plan title override. When omitted, the tool derives one from the document.", Required: false},
			{Name: "id", Type: "string", Description: "Optional existing plan id from plan_list. Pass this to refresh an existing plan instead of creating a new one.", Required: false},
			{Name: "status", Type: "string", Description: "Optional status override. Defaults to active.", Required: false},
			{Name: "category", Type: "string", Description: "Optional plan category such as work, health, finance, or social.", Required: false},
			{Name: "tags", Type: "array", Description: "Optional list of free-form tags for grouping and filtering.", Required: false},
			{Name: "data_sources", Type: "array", Description: "Optional list of systems or apps this plan depends on.", Required: false},
			{Name: "review_cadence", Type: "string", Description: "Optional review cadence such as daily, weekday-morning, or weekly.", Required: false},
			{Name: "metrics", Type: "object", Description: "Optional structured metrics map associated with the imported plan.", Required: false},
			{Name: "source", Type: "string", Description: "Optional source label, for example the upstream system or document name.", Required: false},
		},
	}
}

func (t *PlanIngestTextTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("plan store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot ingest plan")
	}

	rawText, _ := params["text"].(string)
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return nil, errors.New("text is required")
	}

	title, _ := params["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		title = derivePlanTitle(rawText)
	}
	if title == "" {
		return nil, errors.New("unable to derive plan title; provide title explicitly")
	}

	id, _ := params["id"].(string)
	id = strings.TrimSpace(id)
	created := false
	if id == "" {
		id = newIngestedPlanID()
		created = true
	}

	status, _ := params["status"].(string)
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}
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

	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)
	if source != "" && !containsString(dataSources, source) {
		dataSources = append(dataSources, source)
	}

	summary := derivePlanSummary(rawText, title, source)
	steps := derivePlanSteps(rawText)
	plan := &store.UserPlan{
		ID:            id,
		UserID:        uid,
		Title:         title,
		Status:        status,
		Target:        title,
		Category:      category,
		Tags:          tags,
		DataSources:   dataSources,
		ReviewCadence: reviewCadence,
		Summary:       summary,
		Metrics:       metrics,
		Steps:         steps,
	}
	store.NormalizeUserPlan(plan)
	if err := t.Store.UpsertUserPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("ingest plan: %w", err)
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
			"target":         plan.Target,
			"vision":         plan.Vision,
			"milestones":     plan.Milestones,
			"progress":       plan.Progress,
			"steps":          plan.Steps,
			"step_count":     len(plan.Steps),
			"source":         source,
		},
	}, nil
}

func derivePlanTitle(raw string) string {
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := headingPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			return trimTitle(match[1])
		}
		if !looksLikeListItem(trimmed) {
			return trimTitle(trimmed)
		}
	}
	return ""
}

func derivePlanSummary(raw, title, source string) string {
	lines := strings.Split(raw, "\n")
	summaryLines := make([]string, 0, 4)
	normalizedTitle := normalizeForCompare(title)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(summaryLines) > 0 {
				break
			}
			continue
		}
		if match := headingPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			trimmed = strings.TrimSpace(match[1])
		}
		if looksLikeListItem(trimmed) {
			if len(summaryLines) > 0 {
				break
			}
			continue
		}
		if normalizeForCompare(trimmed) == normalizedTitle {
			continue
		}
		summaryLines = append(summaryLines, trimmed)
		if len(summaryLines) >= 3 {
			break
		}
	}
	summary := strings.Join(summaryLines, " ")
	summary = strings.TrimSpace(summary)
	if source != "" {
		if summary == "" {
			summary = fmt.Sprintf("Imported from %s.", source)
		} else {
			summary = fmt.Sprintf("%s Imported from %s.", summary, source)
		}
	}
	return summary
}

func derivePlanSteps(raw string) []map[string]any {
	lines := strings.Split(raw, "\n")
	steps := make([]map[string]any, 0, 8)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case checkboxStepPattern.MatchString(trimmed):
			match := checkboxStepPattern.FindStringSubmatch(trimmed)
			status := "todo"
			if len(match) == 3 && strings.EqualFold(strings.TrimSpace(match[1]), "x") {
				status = "done"
			}
			body := strings.TrimSpace(match[2])
			if body == "" {
				continue
			}
			steps = append(steps, map[string]any{
				"title":  body,
				"status": status,
			})
		case bulletStepPattern.MatchString(trimmed):
			match := bulletStepPattern.FindStringSubmatch(trimmed)
			body := strings.TrimSpace(match[1])
			if body == "" {
				continue
			}
			steps = append(steps, map[string]any{
				"title":  body,
				"status": inferStepStatus(body),
			})
		}
	}
	return steps
}

func inferStepStatus(body string) string {
	lower := strings.ToLower(strings.TrimSpace(body))
	switch {
	case strings.HasPrefix(lower, "done:"),
		strings.HasPrefix(lower, "completed:"),
		strings.Contains(lower, " (done)"),
		strings.Contains(lower, " [done]"):
		return "done"
	case strings.HasPrefix(lower, "doing:"),
		strings.HasPrefix(lower, "in progress:"),
		strings.Contains(lower, " (doing)"),
		strings.Contains(lower, " [doing]"):
		return "doing"
	default:
		return "todo"
	}
}

func looksLikeListItem(line string) bool {
	return checkboxStepPattern.MatchString(line) || bulletStepPattern.MatchString(line)
}

func trimTitle(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "#*-• \t")
	if len(value) > 120 {
		return strings.TrimSpace(value[:120])
	}
	return value
}

func normalizeForCompare(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(value, "#*-• \t")))
}

func newIngestedPlanID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "plan-import-fallback"
	}
	return "plan-" + hex.EncodeToString(b[:])
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
