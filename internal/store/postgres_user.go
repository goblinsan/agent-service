package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (p *Postgres) EnsureUser(ctx context.Context, id, displayName string) error {
	if id == "" {
		return errors.New("user id required")
	}
	name := displayName
	if name == "" {
		name = id
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO users (id, display_name) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`,
		id, name,
	)
	return err
}

func (p *Postgres) ListUserMemories(ctx context.Context, userID string) ([]UserMemory, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT key, value, confidence, updated_at
		 FROM user_memories WHERE user_id = $1
		 ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.Key, &m.Value, &m.Confidence, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertUserMemory(ctx context.Context, userID, key, value string, confidence float64) error {
	if userID == "" || key == "" {
		return errors.New("user_id and key required")
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO user_memories (user_id, key, value, confidence, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, key)
		 DO UPDATE SET value = EXCLUDED.value,
		               confidence = EXCLUDED.confidence,
		               updated_at = NOW()`,
		userID, key, value, confidence,
	)
	return err
}

func (p *Postgres) DeleteUserMemory(ctx context.Context, userID, key string) error {
	if userID == "" || key == "" {
		return errors.New("user_id and key required")
	}
	_, err := p.db.ExecContext(ctx,
		`DELETE FROM user_memories WHERE user_id = $1 AND key = $2`,
		userID, key,
	)
	return err
}

func (p *Postgres) AppendUserEvent(ctx context.Context, evt *UserEvent) error {
	if evt == nil || evt.UserID == "" {
		return errors.New("user event requires user_id")
	}
	payload := evt.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var sessionID sql.NullString
	if evt.SessionID != "" {
		sessionID = sql.NullString{String: evt.SessionID, Valid: true}
	}
	return p.db.QueryRowContext(ctx,
		`INSERT INTO user_events (user_id, session_id, kind, source, summary, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		evt.UserID, sessionID, evt.Kind, evt.Source, evt.Summary, string(raw),
	).Scan(&evt.ID, &evt.CreatedAt)
}

func (p *Postgres) ListRecentUserEvents(ctx context.Context, userID string, limit int) ([]UserEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, COALESCE(session_id, ''), kind, source, summary, payload, created_at
		 FROM user_events WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserEvent
	for rows.Next() {
		var e UserEvent
		var payloadRaw []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.SessionID, &e.Kind, &e.Source, &e.Summary, &payloadRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadRaw) > 0 {
			if err := json.Unmarshal(payloadRaw, &e.Payload); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertUserPlan(ctx context.Context, plan *UserPlan) error {
	if plan == nil || plan.ID == "" || plan.UserID == "" {
		return errors.New("plan id and user_id required")
	}
	NormalizeUserPlan(plan)
	steps := plan.Steps
	if steps == nil {
		steps = []map[string]any{}
	}
	stepsRaw, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	tags := plan.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsRaw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	dataSources := plan.DataSources
	if dataSources == nil {
		dataSources = []string{}
	}
	dataSourcesRaw, err := json.Marshal(dataSources)
	if err != nil {
		return err
	}
	connectors := plan.Connectors
	if connectors == nil {
		connectors = []PlanConnector{}
	}
	connectorsRaw, err := json.Marshal(connectors)
	if err != nil {
		return err
	}
	metrics := plan.Metrics
	if metrics == nil {
		metrics = map[string]any{}
	}
	metricsRaw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	objectives := plan.Objectives
	if objectives == nil {
		objectives = []string{}
	}
	objectivesRaw, err := json.Marshal(objectives)
	if err != nil {
		return err
	}
	principles := plan.Principles
	if principles == nil {
		principles = []string{}
	}
	principlesRaw, err := json.Marshal(principles)
	if err != nil {
		return err
	}
	trackedMetrics := plan.TrackedMetrics
	if trackedMetrics == nil {
		trackedMetrics = []PlanTrackedMetric{}
	}
	trackedMetricsRaw, err := json.Marshal(trackedMetrics)
	if err != nil {
		return err
	}
	baselineFacts := plan.BaselineFacts
	if baselineFacts == nil {
		baselineFacts = []PlanFact{}
	}
	baselineFactsRaw, err := json.Marshal(baselineFacts)
	if err != nil {
		return err
	}
	successCriteria := plan.SuccessCriteria
	if successCriteria == nil {
		successCriteria = []string{}
	}
	successCriteriaRaw, err := json.Marshal(successCriteria)
	if err != nil {
		return err
	}
	cadence := plan.Cadence
	if cadence == nil {
		cadence = []PlanCadenceEntry{}
	}
	cadenceRaw, err := json.Marshal(cadence)
	if err != nil {
		return err
	}
	supportingSections := plan.SupportingSections
	if supportingSections == nil {
		supportingSections = []PlanSupportingSection{}
	}
	supportingSectionsRaw, err := json.Marshal(supportingSections)
	if err != nil {
		return err
	}
	milestones := plan.Milestones
	if milestones == nil {
		milestones = []UserPlanMilestone{}
	}
	milestonesRaw, err := json.Marshal(milestones)
	if err != nil {
		return err
	}
	progressRaw, err := json.Marshal(plan.Progress)
	if err != nil {
		return err
	}
	status := plan.Status
	if status == "" {
		status = "draft"
	}
	result, err := p.db.ExecContext(ctx,
		`INSERT INTO user_plans (
		     id, user_id, title, status, vision, target, category, objectives, principles, tags, data_sources,
		     connectors, review_cadence, summary, metrics, tracked_metrics, baseline_facts, success_criteria,
		     cadence, supporting_sections, milestones, progress, steps, created_at, updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, NOW(), NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   title = EXCLUDED.title,
		   status = EXCLUDED.status,
		   vision = EXCLUDED.vision,
		   target = EXCLUDED.target,
		   category = EXCLUDED.category,
		   objectives = EXCLUDED.objectives,
		   principles = EXCLUDED.principles,
		   tags = EXCLUDED.tags,
		   data_sources = EXCLUDED.data_sources,
		   connectors = EXCLUDED.connectors,
		   review_cadence = EXCLUDED.review_cadence,
		   summary = EXCLUDED.summary,
		   metrics = EXCLUDED.metrics,
		   tracked_metrics = EXCLUDED.tracked_metrics,
		   baseline_facts = EXCLUDED.baseline_facts,
		   success_criteria = EXCLUDED.success_criteria,
		   cadence = EXCLUDED.cadence,
		   supporting_sections = EXCLUDED.supporting_sections,
		   milestones = EXCLUDED.milestones,
		   progress = EXCLUDED.progress,
		   steps = EXCLUDED.steps,
		   updated_at = NOW()
		 WHERE user_plans.user_id = EXCLUDED.user_id`,
		plan.ID,
		plan.UserID,
		plan.Title,
		status,
		plan.Vision,
		plan.Target,
		plan.Category,
		string(objectivesRaw),
		string(principlesRaw),
		string(tagsRaw),
		string(dataSourcesRaw),
		string(connectorsRaw),
		plan.ReviewCadence,
		nullableString(plan.Summary),
		string(metricsRaw),
		string(trackedMetricsRaw),
		string(baselineFactsRaw),
		string(successCriteriaRaw),
		string(cadenceRaw),
		string(supportingSectionsRaw),
		string(milestonesRaw),
		string(progressRaw),
		string(stepsRaw),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrForbidden
	}
	return nil
}

func (p *Postgres) ListActivePlans(ctx context.Context, userID string) ([]UserPlan, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, title, status, COALESCE(vision, ''), COALESCE(target, ''),
		        COALESCE(category, ''), objectives, principles, tags, data_sources,
		        connectors, COALESCE(review_cadence, ''), COALESCE(summary, ''), metrics, tracked_metrics,
		        baseline_facts, success_criteria, cadence, supporting_sections, milestones, progress, steps,
		        created_at, updated_at
		 FROM user_plans
		 WHERE user_id = $1 AND status NOT IN ('done', 'abandoned')
		 ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserPlan
	for rows.Next() {
		plan, err := scanUserPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (p *Postgres) GetUserPlan(ctx context.Context, userID, planID string) (*UserPlan, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, title, status, COALESCE(vision, ''), COALESCE(target, ''),
		        COALESCE(category, ''), objectives, principles, tags, data_sources, connectors,
		        COALESCE(review_cadence, ''), COALESCE(summary, ''), metrics, tracked_metrics, baseline_facts,
		        success_criteria, cadence, supporting_sections, milestones, progress, steps,
		        created_at, updated_at
		 FROM user_plans
		 WHERE user_id = $1 AND id = $2
		 LIMIT 1`,
		userID, planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	plan, err := scanUserPlan(rows)
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func (p *Postgres) DeleteUserPlan(ctx context.Context, userID, planID string) error {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM user_plans WHERE user_id = $1 AND id = $2`,
		userID, planID,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanUserPlan(rows *sql.Rows) (UserPlan, error) {
	var plan UserPlan
	var tagsRaw []byte
	var dataSourcesRaw []byte
	var connectorsRaw []byte
	var objectivesRaw []byte
	var principlesRaw []byte
	var metricsRaw []byte
	var trackedMetricsRaw []byte
	var baselineFactsRaw []byte
	var successCriteriaRaw []byte
	var cadenceRaw []byte
	var supportingSectionsRaw []byte
	var milestonesRaw []byte
	var progressRaw []byte
	var stepsRaw []byte
	if err := rows.Scan(
		&plan.ID,
		&plan.UserID,
		&plan.Title,
		&plan.Status,
		&plan.Vision,
		&plan.Target,
		&plan.Category,
		&objectivesRaw,
		&principlesRaw,
		&tagsRaw,
		&dataSourcesRaw,
		&connectorsRaw,
		&plan.ReviewCadence,
		&plan.Summary,
		&metricsRaw,
		&trackedMetricsRaw,
		&baselineFactsRaw,
		&successCriteriaRaw,
		&cadenceRaw,
		&supportingSectionsRaw,
		&milestonesRaw,
		&progressRaw,
		&stepsRaw,
		&plan.CreatedAt,
		&plan.UpdatedAt,
	); err != nil {
		return UserPlan{}, err
	}
	if len(tagsRaw) > 0 {
		if err := json.Unmarshal(tagsRaw, &plan.Tags); err != nil {
			return UserPlan{}, err
		}
	}
	if len(objectivesRaw) > 0 {
		if err := json.Unmarshal(objectivesRaw, &plan.Objectives); err != nil {
			return UserPlan{}, err
		}
	}
	if len(principlesRaw) > 0 {
		if err := json.Unmarshal(principlesRaw, &plan.Principles); err != nil {
			return UserPlan{}, err
		}
	}
	if len(dataSourcesRaw) > 0 {
		if err := json.Unmarshal(dataSourcesRaw, &plan.DataSources); err != nil {
			return UserPlan{}, err
		}
	}
	if len(connectorsRaw) > 0 {
		if err := json.Unmarshal(connectorsRaw, &plan.Connectors); err != nil {
			return UserPlan{}, err
		}
	}
	if len(metricsRaw) > 0 {
		if err := json.Unmarshal(metricsRaw, &plan.Metrics); err != nil {
			return UserPlan{}, err
		}
	}
	if len(trackedMetricsRaw) > 0 {
		if err := json.Unmarshal(trackedMetricsRaw, &plan.TrackedMetrics); err != nil {
			return UserPlan{}, err
		}
	}
	if len(baselineFactsRaw) > 0 {
		if err := json.Unmarshal(baselineFactsRaw, &plan.BaselineFacts); err != nil {
			return UserPlan{}, err
		}
	}
	if len(successCriteriaRaw) > 0 {
		if err := json.Unmarshal(successCriteriaRaw, &plan.SuccessCriteria); err != nil {
			return UserPlan{}, err
		}
	}
	if len(cadenceRaw) > 0 {
		if err := json.Unmarshal(cadenceRaw, &plan.Cadence); err != nil {
			return UserPlan{}, err
		}
	}
	if len(supportingSectionsRaw) > 0 {
		if err := json.Unmarshal(supportingSectionsRaw, &plan.SupportingSections); err != nil {
			return UserPlan{}, err
		}
	}
	if len(milestonesRaw) > 0 {
		if err := json.Unmarshal(milestonesRaw, &plan.Milestones); err != nil {
			return UserPlan{}, err
		}
	}
	if len(progressRaw) > 0 {
		if err := json.Unmarshal(progressRaw, &plan.Progress); err != nil {
			return UserPlan{}, err
		}
	}
	if len(stepsRaw) > 0 {
		if err := json.Unmarshal(stepsRaw, &plan.Steps); err != nil {
			return UserPlan{}, err
		}
	}
	NormalizeUserPlan(&plan)
	return plan, nil
}
