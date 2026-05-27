package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (p *Postgres) CreateNotification(ctx context.Context, n *Notification) error {
	if n == nil || n.ID == "" || n.UserID == "" {
		return errors.New("notification id and user_id required")
	}
	if n.Kind == "" {
		n.Kind = "generic"
	}
	payload := n.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.db.QueryRowContext(ctx,
		`INSERT INTO notifications (id, user_id, kind, title, body, thread_id, source_run_id, payload, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
		 RETURNING created_at`,
		n.ID, n.UserID, n.Kind, n.Title, nullableString(n.Body), nullableString(n.ThreadID), nullableString(n.SourceRunID), string(raw), nullableTime(n.CreatedAt),
	).Scan(&n.CreatedAt)
}

func (p *Postgres) ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, user_id, kind, title, COALESCE(body, ''), COALESCE(thread_id, ''), COALESCE(source_run_id, ''), payload, read_at, dismissed_at, created_at
		 FROM notifications
		 WHERE user_id = $1
		   AND dismissed_at IS NULL`
	args := []any{userID}
	if unreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC LIMIT $2`
	args = append(args, limit)

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Notification, 0, limit)
	for rows.Next() {
		var n Notification
		var payloadRaw []byte
		var readAt, dismissedAt sql.NullTime
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Title, &n.Body, &n.ThreadID, &n.SourceRunID, &payloadRaw, &readAt, &dismissedAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadRaw) > 0 {
			if err := json.Unmarshal(payloadRaw, &n.Payload); err != nil {
				return nil, err
			}
		}
		if readAt.Valid {
			t := readAt.Time
			n.ReadAt = &t
		}
		if dismissedAt.Valid {
			t := dismissedAt.Time
			n.DismissedAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) MarkNotificationRead(ctx context.Context, userID, notificationID string) error {
	res, err := p.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, NOW())
		 WHERE id = $1 AND user_id = $2`,
		notificationID, userID,
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

func (p *Postgres) MarkAllNotificationsRead(ctx context.Context, userID string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, NOW())
		 WHERE user_id = $1 AND read_at IS NULL`,
		userID,
	)
	return err
}

func (p *Postgres) DeleteNotification(ctx context.Context, userID, notificationID string) error {
	res, err := p.db.ExecContext(ctx,
		`UPDATE notifications SET dismissed_at = NOW()
		 WHERE id = $1 AND user_id = $2`,
		notificationID, userID,
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

func (p *Postgres) CreateScheduledJob(ctx context.Context, job *ScheduledJob) error {
	if job == nil || job.ID == "" || job.UserID == "" {
		return errors.New("scheduled job id and user_id required")
	}
	if job.Kind == "" {
		job.Kind = "prompt"
	}
	if job.Status == "" {
		job.Status = "pending"
	}
	if job.RunAt.IsZero() {
		job.RunAt = time.Now().UTC()
	}
	payload := job.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.db.QueryRowContext(ctx,
		`INSERT INTO scheduled_jobs (id, user_id, kind, prompt, thread_id, agent_id, payload, run_at, recurrence, timezone, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11, NOW(), NOW())
		 RETURNING created_at, updated_at`,
		job.ID, job.UserID, job.Kind, job.Prompt, nullableString(job.ThreadID), nullableString(job.AgentID), string(raw), job.RunAt, job.Recurrence, job.Timezone, job.Status,
	).Scan(&job.CreatedAt, &job.UpdatedAt)
}

func (p *Postgres) ListScheduledJobs(ctx context.Context, userID string, limit int) ([]ScheduledJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, user_id, kind, prompt, COALESCE(thread_id, ''), COALESCE(agent_id, ''), payload,
		        run_at, COALESCE(recurrence, ''), COALESCE(timezone, ''), status, locked_until, last_run_at, created_at, updated_at
		 FROM scheduled_jobs
		 WHERE user_id = $1
		 ORDER BY run_at ASC
		 LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduledJob, 0, limit)
	for rows.Next() {
		job, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteScheduledJob(ctx context.Context, userID, jobID string) error {
	res, err := p.db.ExecContext(ctx,
		`UPDATE scheduled_jobs
		 SET status = 'cancelled', updated_at = NOW(), locked_until = NULL
		 WHERE id = $1 AND user_id = $2 AND status IN ('pending', 'running')`,
		jobID, userID,
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

func (p *Postgres) AcquireDueScheduledJobs(ctx context.Context, limit int, lease time.Duration) ([]ScheduledJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	rows, err := p.db.QueryContext(ctx,
		`WITH due AS (
			SELECT id
			FROM scheduled_jobs
			WHERE status = 'pending'
			  AND run_at <= NOW()
			  AND (locked_until IS NULL OR locked_until < NOW())
			ORDER BY run_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE scheduled_jobs s
			SET status = 'running',
			    locked_until = NOW() + $2::interval,
			    updated_at = NOW()
			FROM due
			WHERE s.id = due.id
			RETURNING s.id, s.user_id, s.kind, s.prompt, COALESCE(s.thread_id, ''), COALESCE(s.agent_id, ''), s.payload,
			          s.run_at, COALESCE(s.recurrence, ''), COALESCE(s.timezone, ''), s.status, s.locked_until, s.last_run_at, s.created_at, s.updated_at
		)
		SELECT * FROM claimed`,
		limit, formatInterval(lease),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduledJob, 0, limit)
	for rows.Next() {
		job, err := scanScheduledJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (p *Postgres) MarkScheduledJobResult(ctx context.Context, jobID, status string, lastRunAt time.Time, nextRunAt *time.Time) error {
	if status == "" {
		status = "completed"
	}
	_, err := p.db.ExecContext(ctx,
		`UPDATE scheduled_jobs
		 SET status = $2,
		     last_run_at = $3,
		     run_at = COALESCE($4, run_at),
		     locked_until = NULL,
		     updated_at = NOW()
		 WHERE id = $1`,
		jobID,
		status,
		lastRunAt,
		nullableTimePtr(nextRunAt),
	)
	return err
}

func (p *Postgres) UpsertDeviceToken(ctx context.Context, token *DeviceToken) error {
	if token == nil || token.UserID == "" || token.Token == "" {
		return errors.New("device token user_id and token required")
	}
	platform := strings.ToLower(strings.TrimSpace(token.Platform))
	if platform == "" {
		platform = "ios"
	}
	normalizedToken := strings.ToLower(strings.TrimSpace(token.Token))
	if token.ID == "" {
		token.ID = normalizedToken
	}
	if token.LastSeenAt.IsZero() {
		token.LastSeenAt = time.Now().UTC()
	}
	if token.CreatedAt.IsZero() {
		token.CreatedAt = token.LastSeenAt
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO device_tokens (id, user_id, platform, token, app_version, last_seen_at, created_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
		 ON CONFLICT (user_id, platform, token)
		 DO UPDATE SET app_version = EXCLUDED.app_version,
		               last_seen_at = EXCLUDED.last_seen_at`,
		token.ID,
		token.UserID,
		platform,
		normalizedToken,
		token.AppVersion,
		token.LastSeenAt,
		token.CreatedAt,
	)
	return err
}

func (p *Postgres) DeleteDeviceToken(ctx context.Context, userID, token string) error {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM device_tokens WHERE user_id = $1 AND token = $2`,
		userID,
		strings.ToLower(strings.TrimSpace(token)),
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

func (p *Postgres) ListDeviceTokens(ctx context.Context, userID, platform string) ([]DeviceToken, error) {
	query := `SELECT id, user_id, platform, token, COALESCE(app_version, ''), last_seen_at, created_at
		 FROM device_tokens
		 WHERE user_id = $1`
	args := []any{userID}
	if strings.TrimSpace(platform) != "" {
		query += ` AND platform = $2`
		args = append(args, strings.ToLower(strings.TrimSpace(platform)))
	}
	query += ` ORDER BY last_seen_at DESC`
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DeviceToken, 0, 8)
	for rows.Next() {
		var d DeviceToken
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.Token, &d.AppVersion, &d.LastSeenAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanScheduledJob(rows *sql.Rows) (ScheduledJob, error) {
	var job ScheduledJob
	var payloadRaw []byte
	var lockedUntil, lastRunAt sql.NullTime
	if err := rows.Scan(
		&job.ID,
		&job.UserID,
		&job.Kind,
		&job.Prompt,
		&job.ThreadID,
		&job.AgentID,
		&payloadRaw,
		&job.RunAt,
		&job.Recurrence,
		&job.Timezone,
		&job.Status,
		&lockedUntil,
		&lastRunAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	); err != nil {
		return ScheduledJob{}, err
	}
	if len(payloadRaw) > 0 {
		if err := json.Unmarshal(payloadRaw, &job.Payload); err != nil {
			return ScheduledJob{}, err
		}
	}
	if lockedUntil.Valid {
		t := lockedUntil.Time
		job.LockedUntil = &t
	}
	if lastRunAt.Valid {
		t := lastRunAt.Time
		job.LastRunAt = &t
	}
	return job, nil
}

func nullableTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}

func nullableTimePtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func formatInterval(d time.Duration) string {
	seconds := int(d / time.Second)
	if seconds <= 0 {
		seconds = 30
	}
	return (time.Duration(seconds) * time.Second).String()
}
