package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/store"
	"gopkg.in/yaml.v3"
)

var (
	checkboxStepPattern  = regexp.MustCompile(`^\s*[-*]\s*\[(?P<mark>[ xX])\]\s*(?P<body>.+)$`)
	bulletStepPattern    = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(?P<body>.+)$`)
	headingPattern       = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+)$`)
	actionSectionPattern = regexp.MustCompile(`(?i)(week|phase|milestone|checklist|workout|run|ride|tempo|strength|recovery|baseline|test|assessment|goal|schedule|prep)`)
	infoSectionPattern   = regexp.MustCompile(`(?i)(current baseline|metrics|recovery priorities|important reminder|core principles|success metrics)`)
)

type structuredPlanDocument struct {
	ID                 string                        `yaml:"id,omitempty" json:"id,omitempty"`
	Title              string                        `yaml:"title" json:"title"`
	Status             string                        `yaml:"status,omitempty" json:"status,omitempty"`
	Vision             string                        `yaml:"vision" json:"vision"`
	Target             string                        `yaml:"target" json:"target"`
	Summary            string                        `yaml:"summary" json:"summary"`
	Category           string                        `yaml:"category" json:"category"`
	Objectives         []string                      `yaml:"objectives" json:"objectives"`
	Principles         []string                      `yaml:"principles" json:"principles"`
	Tags               []string                      `yaml:"tags" json:"tags"`
	DataSources        []string                      `yaml:"data_sources" json:"data_sources"`
	Connectors         []store.PlanConnector         `yaml:"connectors,omitempty" json:"connectors,omitempty"`
	ReviewCadence      string                        `yaml:"review_cadence" json:"review_cadence"`
	Metrics            map[string]any                `yaml:"metrics" json:"metrics"`
	TrackedMetrics     []structuredTrackedMetric     `yaml:"tracked_metrics" json:"tracked_metrics"`
	BaselineFacts      []structuredFact              `yaml:"baseline_facts" json:"baseline_facts"`
	SuccessCriteria    []string                      `yaml:"success_criteria" json:"success_criteria"`
	Cadence            []structuredCadenceEntry      `yaml:"cadence" json:"cadence"`
	SupportingSections []structuredSupportingSection `yaml:"supporting_sections" json:"supporting_sections"`
	Milestones         []structuredPlanMilestone     `yaml:"milestones" json:"milestones"`
	Steps              []any                         `yaml:"steps" json:"steps"`
}

type structuredTrackedMetric struct {
	Name     string `yaml:"name" json:"name"`
	Notes    string `yaml:"notes" json:"notes"`
	Source   string `yaml:"source" json:"source"`
	Cadence  string `yaml:"cadence" json:"cadence"`
	Baseline string `yaml:"baseline" json:"baseline"`
	Target   string `yaml:"target" json:"target"`
}

type structuredFact struct {
	Label string `yaml:"label" json:"label"`
	Value string `yaml:"value" json:"value"`
}

type structuredCadenceEntry struct {
	Label    string `yaml:"label" json:"label"`
	Day      string `yaml:"day" json:"day"`
	Activity string `yaml:"activity" json:"activity"`
	Notes    string `yaml:"notes" json:"notes"`
}

type structuredSupportingSection struct {
	Title   string                     `yaml:"title" json:"title"`
	Kind    string                     `yaml:"kind" json:"kind"`
	Summary string                     `yaml:"summary" json:"summary"`
	Items   []structuredSupportingItem `yaml:"items" json:"items"`
}

type structuredSupportingItem struct {
	Label   string `yaml:"label" json:"label"`
	Kind    string `yaml:"kind" json:"kind"`
	Content string `yaml:"content" json:"content"`
	URI     string `yaml:"uri" json:"uri"`
}

type structuredPlanMilestone struct {
	ID            string               `yaml:"id" json:"id"`
	Title         string               `yaml:"title" json:"title"`
	Status        string               `yaml:"status" json:"status"`
	Summary       string               `yaml:"summary" json:"summary"`
	ScheduledDate *time.Time           `yaml:"scheduled_date,omitempty" json:"scheduled_date,omitempty"`
	StartDate     *time.Time           `yaml:"start_date,omitempty" json:"start_date,omitempty"`
	TargetDate    *time.Time           `yaml:"target_date,omitempty" json:"target_date,omitempty"`
	EndDate       *time.Time           `yaml:"end_date,omitempty" json:"end_date,omitempty"`
	DependsOn     []string             `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Sequence      int                  `yaml:"sequence,omitempty" json:"sequence,omitempty"`
	Tasks         []structuredPlanTask `yaml:"tasks" json:"tasks"`
}

type structuredPlanTask struct {
	ID          string     `yaml:"id" json:"id"`
	Title       string     `yaml:"title" json:"title"`
	Status      string     `yaml:"status" json:"status"`
	Notes       string     `yaml:"notes" json:"notes"`
	ScheduledAt *time.Time `yaml:"scheduled_at,omitempty" json:"scheduled_at,omitempty"`
	StartAt     *time.Time `yaml:"start_at,omitempty" json:"start_at,omitempty"`
	TargetAt    *time.Time `yaml:"target_at,omitempty" json:"target_at,omitempty"`
	DueAt       *time.Time `yaml:"due_at,omitempty" json:"due_at,omitempty"`
	EndAt       *time.Time `yaml:"end_at,omitempty" json:"end_at,omitempty"`
	DependsOn   []string   `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Sequence    int        `yaml:"sequence,omitempty" json:"sequence,omitempty"`
	CompletedAt *time.Time `yaml:"completed_at,omitempty" json:"completed_at,omitempty"`
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
	lookupParams := clonePlanParams(params)
	requestedID, _ := lookupParams["id"].(string)
	if strings.TrimSpace(requestedID) == "" {
		if title := candidatePlanTitleForLookup(lookupParams); strings.TrimSpace(title) != "" {
			resolvedID, created, err := resolvePlanIdentity(ctx, t.Store, uid, "", title)
			if err != nil {
				return nil, err
			}
			if !created && strings.TrimSpace(resolvedID) != "" {
				lookupParams["id"] = resolvedID
			}
		}
	}
	plan, created, source, err := BuildUserPlanFromDocument(uid, lookupParams)
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
			"id":                  plan.ID,
			"title":               plan.Title,
			"status":              plan.Status,
			"category":            plan.Category,
			"objectives":          plan.Objectives,
			"principles":          plan.Principles,
			"tags":                plan.Tags,
			"data_sources":        plan.DataSources,
			"connectors":          plan.Connectors,
			"review_cadence":      plan.ReviewCadence,
			"summary":             plan.Summary,
			"metrics":             plan.Metrics,
			"tracked_metrics":     plan.TrackedMetrics,
			"baseline_facts":      plan.BaselineFacts,
			"success_criteria":    plan.SuccessCriteria,
			"cadence":             plan.Cadence,
			"supporting_sections": plan.SupportingSections,
			"target":              plan.Target,
			"vision":              plan.Vision,
			"milestones":          plan.Milestones,
			"progress":            plan.Progress,
			"steps":               plan.Steps,
			"step_count":          len(plan.Steps),
			"source":              source,
		},
	}, nil
}

func clonePlanParams(params map[string]any) map[string]any {
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}

func candidatePlanTitleForLookup(params map[string]any) string {
	rawText, _ := params["text"].(string)
	rawText = strings.TrimSpace(rawText)
	explicitTitle, _ := params["title"].(string)
	explicitTitle = strings.TrimSpace(explicitTitle)
	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)

	if doc, ok, _ := parseStructuredPlanDocument(rawText); ok && strings.TrimSpace(doc.Title) != "" {
		return strings.TrimSpace(doc.Title)
	}
	if explicitTitle != "" && !expectsStructuredPlanDocument(explicitTitle) {
		return explicitTitle
	}
	if source != "" && !expectsStructuredPlanDocument(source) && explicitTitle == "" {
		return source
	}
	return derivePlanTitle(rawText)
}

func StructuredPlanDocumentID(raw string) string {
	doc, ok, err := parseStructuredPlanDocument(raw)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(doc.ID)
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
	providedID := id
	created := false
	if id == "" {
		id = newIngestedPlanID()
		created = true
	}

	status, _ := params["status"].(string)
	status = strings.TrimSpace(status)
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
	expectsStructured := expectsStructuredPlanDocument(source) || expectsStructuredPlanDocument(title)
	if source != "" && !containsString(dataSources, source) {
		dataSources = append(dataSources, source)
	}
	dataSources = mergeDataSourcesWithConnectors(dataSources, connectors)
	if category == "" {
		category = inferCategoryFromConnectors(connectors)
	}

	summary := derivePlanSummary(rawText, title, source)
	var steps []map[string]any
	var milestones []store.UserPlanMilestone
	if doc, ok, parseErr := parseStructuredPlanDocument(rawText); ok {
		if providedID == "" && strings.TrimSpace(doc.ID) != "" {
			id = strings.TrimSpace(doc.ID)
			created = false
		}
		if strings.TrimSpace(doc.Title) != "" {
			title = strings.TrimSpace(doc.Title)
		}
		if status == "" && strings.TrimSpace(doc.Status) != "" {
			status = strings.TrimSpace(doc.Status)
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
		if len(connectors) == 0 && len(doc.Connectors) > 0 {
			connectors = doc.Connectors
		}
		dataSources = mergeDataSourcesWithConnectors(dataSources, connectors)
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
		plan := &store.UserPlan{
			ID:                 id,
			UserID:             userID,
			Title:              title,
			Status:             status,
			Vision:             strings.TrimSpace(doc.Vision),
			Target:             firstNonEmptyString(strings.TrimSpace(doc.Target), title),
			Category:           category,
			Objectives:         normalizeStringSlice(doc.Objectives),
			Principles:         normalizeStringSlice(doc.Principles),
			Tags:               tags,
			DataSources:        dataSources,
			Connectors:         connectors,
			ReviewCadence:      reviewCadence,
			Summary:            summary,
			Metrics:            metrics,
			TrackedMetrics:     structuredTrackedMetricsToStore(doc.TrackedMetrics),
			BaselineFacts:      structuredFactsToStore(doc.BaselineFacts),
			SuccessCriteria:    normalizeStringSlice(doc.SuccessCriteria),
			Cadence:            structuredCadenceToStore(doc.Cadence),
			SupportingSections: structuredSupportingSectionsToStore(doc.SupportingSections),
			Milestones:         milestones,
			Steps:              steps,
		}
		if strings.TrimSpace(plan.Status) == "" {
			plan.Status = "active"
		}
		return plan, created, source, nil
	} else if expectsStructured {
		if parseErr != nil {
			return nil, false, "", fmt.Errorf("failed to parse structured plan document %q: %v. Fix the YAML/JSON formatting and try again. For YAML, quote values containing ':' such as task titles", sourceOrTitle(source, title), parseErr)
		}
		return nil, false, "", fmt.Errorf("failed to parse structured plan document %q: missing required top-level fields such as title. Fix the YAML/JSON structure and try again", sourceOrTitle(source, title))
	} else {
		milestones = derivePlanMilestones(rawText)
		if len(milestones) > 0 {
			steps = nil
		} else {
			steps = derivePlanSteps(rawText)
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
	if strings.TrimSpace(plan.Status) == "" {
		plan.Status = "active"
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

func parseStructuredPlanDocument(raw string) (structuredPlanDocument, bool, error) {
	var doc structuredPlanDocument
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return doc, false, nil
	}
	if !strings.Contains(trimmed, "title:") && !strings.Contains(trimmed, "\"title\"") {
		return doc, false, nil
	}
	if err := yaml.Unmarshal([]byte(trimmed), &doc); err != nil {
		return structuredPlanDocument{}, false, err
	}
	if strings.TrimSpace(doc.Title) == "" {
		return structuredPlanDocument{}, false, nil
	}
	return doc, true, nil
}

func expectsStructuredPlanDocument(name string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

func sourceOrTitle(source, title string) string {
	if strings.TrimSpace(source) != "" {
		return strings.TrimSpace(source)
	}
	return strings.TrimSpace(title)
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
				ID:          taskID,
				Title:       taskTitle,
				Status:      firstNonEmptyString(strings.TrimSpace(task.Status), "todo"),
				Notes:       strings.TrimSpace(task.Notes),
				ScheduledAt: task.ScheduledAt,
				StartAt:     task.StartAt,
				TargetAt:    task.TargetAt,
				DueAt:       task.DueAt,
				EndAt:       task.EndAt,
				DependsOn:   normalizeStringSlice(task.DependsOn),
				Sequence:    task.Sequence,
				CompletedAt: task.CompletedAt,
			})
		}
		out = append(out, store.UserPlanMilestone{
			ID:            id,
			Title:         title,
			Status:        firstNonEmptyString(strings.TrimSpace(milestone.Status), "todo"),
			Summary:       strings.TrimSpace(milestone.Summary),
			ScheduledDate: milestone.ScheduledDate,
			StartDate:     milestone.StartDate,
			TargetDate:    milestone.TargetDate,
			EndDate:       milestone.EndDate,
			DependsOn:     normalizeStringSlice(milestone.DependsOn),
			Sequence:      milestone.Sequence,
			Tasks:         tasks,
		})
	}
	return out
}

func structuredDocumentFromPlan(plan store.UserPlan) structuredPlanDocument {
	doc := structuredPlanDocument{
		ID:                 strings.TrimSpace(plan.ID),
		Title:              strings.TrimSpace(plan.Title),
		Status:             strings.TrimSpace(plan.Status),
		Vision:             strings.TrimSpace(plan.Vision),
		Target:             strings.TrimSpace(plan.Target),
		Summary:            strings.TrimSpace(plan.Summary),
		Category:           strings.TrimSpace(plan.Category),
		Objectives:         append([]string(nil), plan.Objectives...),
		Principles:         append([]string(nil), plan.Principles...),
		Tags:               append([]string(nil), plan.Tags...),
		DataSources:        append([]string(nil), plan.DataSources...),
		Connectors:         append([]store.PlanConnector(nil), plan.Connectors...),
		ReviewCadence:      strings.TrimSpace(plan.ReviewCadence),
		Metrics:            plan.Metrics,
		TrackedMetrics:     structuredTrackedMetricsFromStore(plan.TrackedMetrics),
		BaselineFacts:      structuredFactsFromStore(plan.BaselineFacts),
		SuccessCriteria:    append([]string(nil), plan.SuccessCriteria...),
		Cadence:            structuredCadenceFromStore(plan.Cadence),
		SupportingSections: structuredSupportingSectionsFromStore(plan.SupportingSections),
		Milestones:         structuredMilestonesFromStore(plan.Milestones),
		Steps:              structuredStepsFromStore(plan.Steps),
	}
	if doc.Metrics == nil {
		doc.Metrics = map[string]any{}
	}
	return doc
}

func RenderUserPlanDocument(plan store.UserPlan, format string) ([]byte, string, error) {
	doc := structuredDocumentFromPlan(plan)
	switch normalizePlanDocumentFormat(format) {
	case "json":
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, "", err
		}
		return raw, "application/json; charset=utf-8", nil
	case "yaml":
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return nil, "", err
		}
		return raw, "application/yaml; charset=utf-8", nil
	default:
		return nil, "", fmt.Errorf("unsupported plan export format %q", format)
	}
}

func PlanDocumentFilename(plan store.UserPlan, format string) string {
	format = normalizePlanDocumentFormat(format)
	slug := slugifyPlanFilename(firstNonEmptyString(plan.Title, plan.ID, "plan"))
	if strings.TrimSpace(plan.ID) != "" {
		slug = fmt.Sprintf("%s-%s", slug, strings.TrimSpace(plan.ID))
	}
	return slug + "." + format
}

func normalizePlanDocumentFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func slugifyPlanFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "plan"
	}
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "plan"
	}
	return out
}

func structuredTrackedMetricsFromStore(input []store.PlanTrackedMetric) []structuredTrackedMetric {
	out := make([]structuredTrackedMetric, 0, len(input))
	for _, metric := range input {
		out = append(out, structuredTrackedMetric{
			Name:     metric.Name,
			Notes:    metric.Notes,
			Source:   metric.Source,
			Cadence:  metric.Cadence,
			Baseline: metric.Baseline,
			Target:   metric.Target,
		})
	}
	return out
}

func structuredFactsFromStore(input []store.PlanFact) []structuredFact {
	out := make([]structuredFact, 0, len(input))
	for _, fact := range input {
		out = append(out, structuredFact{
			Label: fact.Label,
			Value: fact.Value,
		})
	}
	return out
}

func structuredCadenceFromStore(input []store.PlanCadenceEntry) []structuredCadenceEntry {
	out := make([]structuredCadenceEntry, 0, len(input))
	for _, entry := range input {
		out = append(out, structuredCadenceEntry{
			Label:    entry.Label,
			Day:      entry.Day,
			Activity: entry.Activity,
			Notes:    entry.Notes,
		})
	}
	return out
}

func structuredSupportingSectionsFromStore(input []store.PlanSupportingSection) []structuredSupportingSection {
	out := make([]structuredSupportingSection, 0, len(input))
	for _, section := range input {
		items := make([]structuredSupportingItem, 0, len(section.Items))
		for _, item := range section.Items {
			items = append(items, structuredSupportingItem{
				Label:   item.Label,
				Kind:    item.Kind,
				Content: item.Content,
				URI:     item.URI,
			})
		}
		out = append(out, structuredSupportingSection{
			Title:   section.Title,
			Kind:    section.Kind,
			Summary: section.Summary,
			Items:   items,
		})
	}
	return out
}

func structuredMilestonesFromStore(input []store.UserPlanMilestone) []structuredPlanMilestone {
	out := make([]structuredPlanMilestone, 0, len(input))
	for _, milestone := range input {
		tasks := make([]structuredPlanTask, 0, len(milestone.Tasks))
		for _, task := range milestone.Tasks {
			tasks = append(tasks, structuredPlanTask{
				ID:          task.ID,
				Title:       task.Title,
				Status:      task.Status,
				Notes:       task.Notes,
				ScheduledAt: task.ScheduledAt,
				StartAt:     task.StartAt,
				TargetAt:    task.TargetAt,
				DueAt:       task.DueAt,
				EndAt:       task.EndAt,
				DependsOn:   append([]string(nil), task.DependsOn...),
				Sequence:    task.Sequence,
				CompletedAt: task.CompletedAt,
			})
		}
		out = append(out, structuredPlanMilestone{
			ID:            milestone.ID,
			Title:         milestone.Title,
			Status:        milestone.Status,
			Summary:       milestone.Summary,
			ScheduledDate: milestone.ScheduledDate,
			StartDate:     milestone.StartDate,
			TargetDate:    milestone.TargetDate,
			EndDate:       milestone.EndDate,
			DependsOn:     append([]string(nil), milestone.DependsOn...),
			Sequence:      milestone.Sequence,
			Tasks:         tasks,
		})
	}
	return out
}

func structuredStepsFromStore(input []map[string]any) []any {
	out := make([]any, 0, len(input))
	for _, step := range input {
		if step == nil {
			continue
		}
		cloned := make(map[string]any, len(step))
		for key, value := range step {
			cloned[key] = value
		}
		out = append(out, cloned)
	}
	return out
}

func structuredTrackedMetricsToStore(input []structuredTrackedMetric) []store.PlanTrackedMetric {
	out := make([]store.PlanTrackedMetric, 0, len(input))
	for _, metric := range input {
		name := strings.TrimSpace(metric.Name)
		if name == "" {
			continue
		}
		out = append(out, store.PlanTrackedMetric{
			Name:     name,
			Notes:    strings.TrimSpace(metric.Notes),
			Source:   strings.TrimSpace(metric.Source),
			Cadence:  strings.TrimSpace(metric.Cadence),
			Baseline: strings.TrimSpace(metric.Baseline),
			Target:   strings.TrimSpace(metric.Target),
		})
	}
	return out
}

func structuredFactsToStore(input []structuredFact) []store.PlanFact {
	out := make([]store.PlanFact, 0, len(input))
	for _, fact := range input {
		label := strings.TrimSpace(fact.Label)
		value := strings.TrimSpace(fact.Value)
		if label == "" || value == "" {
			continue
		}
		out = append(out, store.PlanFact{Label: label, Value: value})
	}
	return out
}

func structuredCadenceToStore(input []structuredCadenceEntry) []store.PlanCadenceEntry {
	out := make([]store.PlanCadenceEntry, 0, len(input))
	for _, entry := range input {
		if strings.TrimSpace(entry.Day) == "" && strings.TrimSpace(entry.Activity) == "" && strings.TrimSpace(entry.Label) == "" {
			continue
		}
		out = append(out, store.PlanCadenceEntry{
			Label:    strings.TrimSpace(entry.Label),
			Day:      strings.TrimSpace(entry.Day),
			Activity: strings.TrimSpace(entry.Activity),
			Notes:    strings.TrimSpace(entry.Notes),
		})
	}
	return out
}

func structuredSupportingSectionsToStore(input []structuredSupportingSection) []store.PlanSupportingSection {
	out := make([]store.PlanSupportingSection, 0, len(input))
	for _, section := range input {
		title := strings.TrimSpace(section.Title)
		if title == "" {
			continue
		}
		items := make([]store.PlanSupportingItem, 0, len(section.Items))
		for _, item := range section.Items {
			if strings.TrimSpace(item.Label) == "" && strings.TrimSpace(item.Content) == "" && strings.TrimSpace(item.URI) == "" {
				continue
			}
			items = append(items, store.PlanSupportingItem{
				Label:   strings.TrimSpace(item.Label),
				Kind:    strings.TrimSpace(item.Kind),
				Content: strings.TrimSpace(item.Content),
				URI:     strings.TrimSpace(item.URI),
			})
		}
		out = append(out, store.PlanSupportingSection{
			Title:   title,
			Kind:    strings.TrimSpace(section.Kind),
			Summary: strings.TrimSpace(section.Summary),
			Items:   items,
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

func normalizeStringSlice(input []string) []string {
	out := make([]string, 0, len(input))
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
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
