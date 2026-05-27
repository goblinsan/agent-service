package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

type PersonalDataIngestTool struct {
	Store         store.Store
	App           string
	Domain        string
	ToolName      string
	ToolDesc      string
	PlanTitle     string
	DefaultSource string
	EventKind     string
}

func (t *PersonalDataIngestTool) Definition() Tool {
	return Tool{
		Name:        t.ToolName,
		Description: t.ToolDesc,
		Params: []Param{
			{Name: "summary", Type: "string", Description: "Optional one-line summary for the sync payload.", Required: false},
			{Name: "metrics", Type: "object", Description: "Normalized metric object from the upstream app. Supports object or JSON string.", Required: false},
			{Name: "review_cadence", Type: "string", Description: "Optional plan review cadence override (for example daily or weekly).", Required: false},
			{Name: "source", Type: "string", Description: "Optional source label override. Defaults to the upstream app name.", Required: false},
			{Name: "external_id", Type: "string", Description: "Optional upstream account identifier for this connector.", Required: false},
		},
	}
}

func (t *PersonalDataIngestTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("personal data store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot ingest personal data")
	}

	source, _ := params["source"].(string)
	source = strings.TrimSpace(source)
	if source == "" {
		source = t.DefaultSource
	}
	summary, _ := params["summary"].(string)
	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = defaultPersonalDataSummary(t.App, t.Domain, source, params["metrics"])
	}
	reviewCadence, _ := params["review_cadence"].(string)
	reviewCadence = strings.TrimSpace(reviewCadence)
	if reviewCadence == "" {
		reviewCadence = "daily"
	}
	externalID, _ := params["external_id"].(string)
	externalID = strings.TrimSpace(externalID)
	metrics, err := normalizeObject(params["metrics"], "metrics")
	if err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = map[string]any{}
	}
	metrics["last_sync_source"] = source

	plans, err := t.Store.ListActivePlans(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list plans for personal data ingest: %w", err)
	}
	plan := findPlanForDomainOrConnector(plans, t.Domain, t.App)
	created := false
	if plan == nil {
		created = true
		plan = &store.UserPlan{
			ID:            newPersonalDataPlanID(t.App),
			UserID:        uid,
			Title:         t.PlanTitle,
			Status:        "active",
			Category:      t.Domain,
			Tags:          []string{t.Domain, "personal-data"},
			ReviewCadence: reviewCadence,
		}
	}

	if strings.TrimSpace(plan.ReviewCadence) == "" {
		plan.ReviewCadence = reviewCadence
	}
	if strings.TrimSpace(plan.Category) == "" {
		plan.Category = t.Domain
	}
	plan.Summary = summary
	plan.Metrics = mergeMetrics(plan.Metrics, metrics)
	connector := store.PlanConnector{
		App:        t.App,
		Type:       "personal_data_app",
		Domain:     t.Domain,
		ExternalID: externalID,
	}
	plan.Connectors = appendConnectorIfMissing(plan.Connectors, connector)
	plan.DataSources = mergeDataSourcesWithConnectors(plan.DataSources, plan.Connectors)
	if !containsString(plan.DataSources, source) {
		plan.DataSources = append(plan.DataSources, source)
	}

	if err := t.Store.UpsertUserPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("upsert personal data plan: %w", err)
	}

	event := &store.UserEvent{
		UserID:  uid,
		Kind:    t.EventKind,
		Source:  source,
		Summary: summary,
		Payload: map[string]any{
			"app":      t.App,
			"domain":   t.Domain,
			"plan_id":  plan.ID,
			"metrics":  metrics,
			"created":  created,
			"tool":     t.ToolName,
			"review":   plan.ReviewCadence,
			"category": plan.Category,
		},
	}
	if err := t.Store.AppendUserEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("append personal data event: %w", err)
	}

	return map[string]any{
		"status":  "ok",
		"created": created,
		"plan": map[string]any{
			"id":             plan.ID,
			"title":          plan.Title,
			"category":       plan.Category,
			"data_sources":   plan.DataSources,
			"connectors":     plan.Connectors,
			"review_cadence": plan.ReviewCadence,
			"summary":        plan.Summary,
			"metrics":        plan.Metrics,
		},
		"event": map[string]any{
			"kind":    event.Kind,
			"source":  event.Source,
			"summary": event.Summary,
		},
	}, nil
}

func defaultPersonalDataSummary(app, domain, source string, rawMetrics any) string {
	parts := []string{fmt.Sprintf("%s sync", app)}
	if metricObj, ok := rawMetrics.(map[string]any); ok {
		for _, key := range []string{"workouts", "active_minutes", "steps", "calories", "calories_consumed", "protein_grams"} {
			if v, ok := metricObj[key]; ok {
				if f, ok := readNumeric(v); ok {
					parts = append(parts, fmt.Sprintf("%s=%s", key, trimTrailingZeros(f)))
				}
			}
		}
	}
	if len(parts) == 1 {
		parts = append(parts, fmt.Sprintf("updated %s context", domain))
	}
	if strings.TrimSpace(source) != "" {
		parts = append(parts, fmt.Sprintf("source=%s", source))
	}
	return strings.Join(parts, "; ")
}

func findPlanForDomainOrConnector(plans []store.UserPlan, domain, app string) *store.UserPlan {
	for i := range plans {
		p := &plans[i]
		for _, c := range p.Connectors {
			if strings.EqualFold(strings.TrimSpace(c.App), strings.TrimSpace(app)) {
				return p
			}
		}
		if strings.EqualFold(strings.TrimSpace(p.Category), strings.TrimSpace(domain)) {
			return p
		}
	}
	return nil
}

func mergeMetrics(existing, incoming map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range incoming {
		out[k] = v
	}
	return out
}

func newPersonalDataPlanID(app string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "plan-" + strings.ToLower(strings.TrimSpace(app)) + "-fallback"
	}
	return "plan-" + strings.ToLower(strings.TrimSpace(app)) + "-" + hex.EncodeToString(b[:])
}

func readNumeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func trimTrailingZeros(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
