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
	"gopkg.in/yaml.v3"
)

var (
	checkboxStepPattern = regexp.MustCompile(`^\s*[-*]\s*\[(?P<mark>[ xX])\]\s*(?P<body>.+)$`)
	bulletStepPattern   = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(?P<body>.+)$`)
	headingPattern      = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+)$`)
	actionSectionPattern = regexp.MustCompile(`(?i)(week|phase|milestone|checklist|workout|run|ride|tempo|strength|recovery|baseline|test|assessment|goal|schedule|prep)`)
	infoSectionPattern   = regexp.MustCompile(`(?i)(current baseline|metrics|recovery priorities|important reminder|core principles|success metrics)`)
)

type structuredPlanDocument struct {
	Title         string                     `yaml:"title" json:"title"`
	Vision        string                     `yaml:"vision" json:"vision"`
	Target        string                     `yaml:"target" json:"target"`
	Summary       string                     `yaml:"summary" json:"summary"`
	Category      string                     `yaml:"category" json:"category"`
	Tags          []string                   `yaml:"tags" json:"tags"`
	DataSources   []string                   `yaml:"data_sources" json:"data_sources"`
	ReviewCadence string                     `yaml:"review_cadence" json:"review_cadence"`
	Metrics       map[string]any             `yaml:"metrics" json:"metrics"`
	Milestones    []structuredPlanMilestone  `yaml:"milestones" json:"milestones"`
	Steps         []any                      `yaml:"steps" json:"steps"`
}

type structuredPlanMilestone struct {
	ID      string               `yaml:"id" json:"id"`
	Title   string               `yaml:"title" json:"title"`
	Status  string               `yaml:"status" json:"status"`
	Summary string               `yaml:"summary" json:"summary"`
	Tasks   []structuredPlanTask `yaml:"tasks" json:"tasks"`
}

type structuredPlanTask struct {
	ID     string `yaml:"id" json:"id"`
	Title  string `yaml:"title" json:"title"`
	Status string `yaml:"status" json:"status"`
	Notes  string `yaml:"notes" json:"notes"`
}

// PlanIngestTextTool converts pasted plain-text or markdown plan documents into
// durable user_plans rows so other agents/systems can seed upcoming work.
type PlanIngestTextTool struct {
	Store store.Store
}

type PlanIngestRequest struct {
	Text          string
	Title         string
	ID            string
	Status        string
	Category      string
	Tags          []string
	DataSources   []string
	Connectors    []store.PlanConnector
	ReviewCadence string
	Metrics       map[string]any
	Source        string
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
			{Name: "connectors", Type: "array", Description: "Optional list of connector objects for personal data apps. Each object should include app (required) plus optional type, domain, and external_id.", Required: false},
			{Name: "connector", Type: "object", Description: "Optional single connector object. Useful when ingesting a single app payload.", Required: false},
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
	plan, created, source, err := BuildUserPlanFromDocument(uid, params)
	if err != nil {
		return nil, err
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
			"connectors":     plan.Connectors,
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

func BuildUserPlanFromDocument(userID string, params map[string]any) (*store.UserPlan, bool, string, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, false, "", errors.New("user id is required")
	}

	rawText, _ := params["text"].(string)
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return nil, false, "", errors.New("text is required")
	}

	title, _ := params["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		title = derivePlanTitle(rawText)
	}
	if title == "" {
		return nil, false, "", errors.New("unable to derive plan title; provide title explicitly")
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
		return nil, false, "", err
	}
	dataSources, err := normalizeStringList(params["data_sources"], "data_sources")
	if err != nil {
		return nil, false, "", err
	}
	connectors, err := normalizePlanConnectors(params["connectors"])
	if err != nil {
		return nil, false, "", err
	}
	singleConnector, err := normalizeSinglePlanConnector(params["connector"])
	if err != nil {
		return nil, false, "", err
	}
	connectors = appendConnectorIfMissing(connectors, singleConnector)
	reviewCadence, _ := params["review_cadence"].(string)
	reviewCadence = strings.TrimSpace(reviewCadence)
	metrics, err := normalizeObject(params["metrics"], "metrics")
	if err != nil {
		return nil, false, "", err
	}

	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)
	if source != "" && !containsString(dataSources, source) {
		dataSources = append(dataSources, source)
	}
	dataSources = mergeDataSourcesWithConnectors(dataSources, connectors)
	if category == "" {
		category = inferCategoryFromConnectors(connectors)
	}

	summary := derivePlanSummary(rawText, title, source)
	steps := derivePlanSteps(rawText)
	var milestones []store.UserPlanMilestone
	if doc, ok := parseStructuredPlanDocument(rawText); ok {
		if strings.TrimSpace(doc.Title) != "" {
			title = strings.TrimSpace(doc.Title)
		}
		if strings.TrimSpace(doc.Summary) != "" {
			summary = strings.TrimSpace(doc.Summary)
		}
		if category == "" {
			category = strings.TrimSpace(doc.Category)
		}
		if len(tags) == 0 && len(doc.Tags) > 0 {
			tags = doc.Tags
		}
		if len(dataSources) == 0 && len(doc.DataSources) > 0 {
			dataSources = doc.DataSources
		}
		if reviewCadence == "" {
			reviewCadence = strings.TrimSpace(doc.ReviewCadence)
		}
		if len(metrics) == 0 && len(doc.Metrics) > 0 {
			metrics = doc.Metrics
		}
		milestones = structuredMilestonesToStore(doc.Milestones)
		if len(doc.Steps) > 0 {
			steps = normalizeStructuredSteps(doc.Steps)
		}
	} else {
		milestones = derivePlanMilestones(rawText)
		if len(milestones) > 0 {
			steps = nil
		}
	}
	plan := &store.UserPlan{
		ID:            id,
		UserID:        userID,
		Title:         title,
		Status:        status,
		Target:        title,
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
	return plan, created, source, nil
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

func parseStructuredPlanDocument(raw string) (structuredPlanDocument, bool) {
	var doc structuredPlanDocument
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return doc, false
	}
	if !strings.Contains(trimmed, "title:") && !strings.Contains(trimmed, "\"title\"") {
		return doc, false
	}
	if err := yaml.Unmarshal([]byte(trimmed), &doc); err != nil {
		return structuredPlanDocument{}, false
	}
	if strings.TrimSpace(doc.Title) == "" {
		return structuredPlanDocument{}, false
	}
	return doc, true
}

func structuredMilestonesToStore(input []structuredPlanMilestone) []store.UserPlanMilestone {
	out := make([]store.UserPlanMilestone, 0, len(input))
	for i, milestone := range input {
		title := strings.TrimSpace(milestone.Title)
		if title == "" {
			continue
		}
		id := strings.TrimSpace(milestone.ID)
		if id == "" {
			id = fmt.Sprintf("m%d", i+1)
		}
		tasks := make([]store.UserPlanTask, 0, len(milestone.Tasks))
		for j, task := range milestone.Tasks {
			taskTitle := strings.TrimSpace(task.Title)
			if taskTitle == "" {
				continue
			}
			taskID := strings.TrimSpace(task.ID)
			if taskID == "" {
				taskID = fmt.Sprintf("%s-t%d", id, j+1)
			}
			tasks = append(tasks, store.UserPlanTask{
				ID:     taskID,
				Title:  taskTitle,
				Status: firstNonEmptyString(strings.TrimSpace(task.Status), "todo"),
				Notes:  strings.TrimSpace(task.Notes),
			})
		}
		out = append(out, store.UserPlanMilestone{
			ID:      id,
			Title:   title,
			Status:  firstNonEmptyString(strings.TrimSpace(milestone.Status), "todo"),
			Summary: strings.TrimSpace(milestone.Summary),
			Tasks:   tasks,
		})
	}
	return out
}

func normalizeStructuredSteps(input []any) []map[string]any {
	out := make([]map[string]any, 0, len(input))
	for _, item := range input {
		switch value := item.(type) {
		case string:
			title := strings.TrimSpace(value)
			if title == "" {
				continue
			}
			out = append(out, map[string]any{
				"title":  title,
				"status": inferStepStatus(title),
			})
		case map[string]any:
			out = append(out, value)
		case map[any]any:
			converted := map[string]any{}
			for k, v := range value {
				key, ok := k.(string)
				if !ok {
					continue
				}
				converted[key] = v
			}
			if len(converted) > 0 {
				out = append(out, converted)
			}
		}
	}
	return out
}

func derivePlanMilestones(raw string) []store.UserPlanMilestone {
	const maxMilestones = 8
	const maxTasksPerMilestone = 6

	type section struct {
		title string
		lines []string
	}
	sections := make([]section, 0, 8)
	current := section{}

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := headingPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			if current.title != "" {
				sections = append(sections, current)
			}
			current = section{title: trimTitle(match[1])}
			continue
		}
		if current.title == "" {
			continue
		}
		current.lines = append(current.lines, trimmed)
	}
	if current.title != "" {
		sections = append(sections, current)
	}

	out := make([]store.UserPlanMilestone, 0, maxMilestones)
	for _, section := range sections {
		if len(out) >= maxMilestones {
			break
		}
		title := strings.TrimSpace(section.title)
		if title == "" || strings.EqualFold(title, derivePlanTitle(raw)) {
			continue
		}

		milestone := store.UserPlanMilestone{
			ID:     fmt.Sprintf("m%d", len(out)+1),
			Title:  title,
			Status: "todo",
		}
		if infoSectionPattern.MatchString(title) {
			milestone.Summary = strings.Join(section.lines, " ")
			if len(milestone.Summary) > 240 {
				milestone.Summary = milestone.Summary[:240] + "..."
			}
			out = append(out, milestone)
			continue
		}
		if !actionSectionPattern.MatchString(title) {
			continue
		}

		tasks := make([]store.UserPlanTask, 0, maxTasksPerMilestone)
		for _, line := range section.lines {
			var body string
			switch {
			case checkboxStepPattern.MatchString(line):
				match := checkboxStepPattern.FindStringSubmatch(line)
				if len(match) == 3 {
					body = strings.TrimSpace(match[2])
				}
			case bulletStepPattern.MatchString(line):
				match := bulletStepPattern.FindStringSubmatch(line)
				if len(match) == 2 {
					body = strings.TrimSpace(match[1])
				}
			}
			if body == "" {
				continue
			}
			tasks = append(tasks, store.UserPlanTask{
				ID:     fmt.Sprintf("%s-t%d", milestone.ID, len(tasks)+1),
				Title:  body,
				Status: inferStepStatus(body),
			})
			if len(tasks) >= maxTasksPerMilestone {
				break
			}
		}
		if len(tasks) == 0 && len(section.lines) > 0 {
			milestone.Summary = strings.Join(section.lines, " ")
			if len(milestone.Summary) > 240 {
				milestone.Summary = milestone.Summary[:240] + "..."
			}
		} else {
			milestone.Tasks = tasks
		}
		out = append(out, milestone)
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func normalizeSinglePlanConnector(v any) (store.PlanConnector, error) {
	if v == nil {
		return store.PlanConnector{}, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return store.PlanConnector{}, errors.New("connector must be an object")
	}
	connectors, err := normalizePlanConnectors([]any{obj})
	if err != nil {
		return store.PlanConnector{}, err
	}
	if len(connectors) == 0 {
		return store.PlanConnector{}, nil
	}
	return connectors[0], nil
}

func appendConnectorIfMissing(connectors []store.PlanConnector, extra store.PlanConnector) []store.PlanConnector {
	if strings.TrimSpace(extra.App) == "" {
		return connectors
	}
	for _, c := range connectors {
		if strings.EqualFold(strings.TrimSpace(c.App), strings.TrimSpace(extra.App)) &&
			strings.EqualFold(strings.TrimSpace(c.Type), strings.TrimSpace(extra.Type)) &&
			strings.EqualFold(strings.TrimSpace(c.Domain), strings.TrimSpace(extra.Domain)) {
			return connectors
		}
	}
	return append(connectors, extra)
}

func inferCategoryFromConnectors(connectors []store.PlanConnector) string {
	for _, c := range connectors {
		domain := strings.ToLower(strings.TrimSpace(c.Domain))
		switch domain {
		case "health", "nutrition":
			return domain
		}
	}
	return ""
}
