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
	metrics := plan.Metrics
	if metrics == nil {
		metrics = map[string]any{}
	}
	metricsRaw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	status := plan.Status
	if status == "" {
		status = "draft"
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO user_plans (
		     id, user_id, title, status, category, tags, data_sources,
		     review_cadence, summary, metrics, steps, created_at, updated_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   title = EXCLUDED.title,
		   status = EXCLUDED.status,
		   category = EXCLUDED.category,
		   tags = EXCLUDED.tags,
		   data_sources = EXCLUDED.data_sources,
		   review_cadence = EXCLUDED.review_cadence,
		   summary = EXCLUDED.summary,
		   metrics = EXCLUDED.metrics,
		   steps = EXCLUDED.steps,
		   updated_at = NOW()`,
		plan.ID,
		plan.UserID,
		plan.Title,
		status,
		nullableString(plan.Category),
		string(tagsRaw),
		string(dataSourcesRaw),
		nullableString(plan.ReviewCadence),
		nullableString(plan.Summary),
		string(metricsRaw),
		string(stepsRaw),
	)
	return err
}

func (p *Postgres) ListActivePlans(ctx context.Context, userID string) ([]UserPlan, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, title, status, COALESCE(category, ''), tags, data_sources,
		        COALESCE(review_cadence, ''), COALESCE(summary, ''), metrics, steps,
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
		var p UserPlan
		var tagsRaw []byte
		var dataSourcesRaw []byte
		var metricsRaw []byte
		var stepsRaw []byte
		if err := rows.Scan(
			&p.ID,
			&p.UserID,
			&p.Title,
			&p.Status,
			&p.Category,
			&tagsRaw,
			&dataSourcesRaw,
			&p.ReviewCadence,
			&p.Summary,
			&metricsRaw,
			&stepsRaw,
			&p.CreatedAt,
			&p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if len(tagsRaw) > 0 {
			if err := json.Unmarshal(tagsRaw, &p.Tags); err != nil {
				return nil, err
			}
		}
		if len(dataSourcesRaw) > 0 {
			if err := json.Unmarshal(dataSourcesRaw, &p.DataSources); err != nil {
				return nil, err
			}
		}
		if len(metricsRaw) > 0 {
			if err := json.Unmarshal(metricsRaw, &p.Metrics); err != nil {
				return nil, err
			}
		}
		if len(stepsRaw) > 0 {
			if err := json.Unmarshal(stepsRaw, &p.Steps); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
