package store

import (
	"context"
	"database/sql"
	"errors"
)

// RoutingConfig is the persisted routing assignment that survives restarts.
type RoutingConfig struct {
	ChatNode       string
	AutomationNode string
}

// GetRouting returns the persisted routing config, or (zero, sql.ErrNoRows)
// when the singleton row has not been written yet.
func (p *Postgres) GetRouting(ctx context.Context) (RoutingConfig, error) {
	var rc RoutingConfig
	err := p.db.QueryRowContext(ctx,
		`SELECT chat_node, automation_node FROM routing_config WHERE id = 1`,
	).Scan(&rc.ChatNode, &rc.AutomationNode)
	if err != nil {
		return RoutingConfig{}, err
	}
	return rc, nil
}

// UpsertRouting writes the singleton routing row.
func (p *Postgres) UpsertRouting(ctx context.Context, rc RoutingConfig) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO routing_config (id, chat_node, automation_node, updated_at)
		 VALUES (1, $1, $2, now())
		 ON CONFLICT (id) DO UPDATE SET
		   chat_node = EXCLUDED.chat_node,
		   automation_node = EXCLUDED.automation_node,
		   updated_at = now()`,
		rc.ChatNode, rc.AutomationNode,
	)
	return err
}

// IsNoRows reports whether err is sql.ErrNoRows.  Exposed so callers outside
// this package can branch on "no row yet" without importing database/sql.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
