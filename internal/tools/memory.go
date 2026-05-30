package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

type memoryDeleter interface {
	DeleteUserMemory(ctx context.Context, userID, key string) error
}

// MemoryRecallTool returns the caller's durable memories. The user_id is
// derived from the context (set by the agent loop) and is NOT a parameter, so
// the model cannot read another user's memory.
type MemoryRecallTool struct {
	Store store.Store
}

func (t *MemoryRecallTool) Definition() Tool {
	return Tool{
		Name:        "memory_recall",
		Description: "Returns all durable facts you have written about the current user. Use this if you need to double-check a fact before responding. Takes no parameters.",
	}
}

func (t *MemoryRecallTool) Execute(ctx context.Context, _ map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("memory store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return map[string]any{"memories": []any{}, "note": "no authenticated user on this run"}, nil
	}
	mems, err := t.Store.ListUserMemories(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	out := make([]map[string]any, 0, len(mems))
	for _, m := range mems {
		out = append(out, map[string]any{
			"key":        m.Key,
			"value":      m.Value,
			"confidence": m.Confidence,
			"updated_at": m.UpdatedAt,
		})
	}
	return map[string]any{"user_id": uid, "memories": out}, nil
}

// MemoryWriteTool records a durable fact about the current user. user_id is
// taken from context; key/value/confidence come from the model.
type MemoryWriteTool struct {
	Store store.Store
}

func (t *MemoryWriteTool) Definition() Tool {
	return Tool{
		Name:        "memory_write",
		Description: "Records a single durable fact about the current user that should persist across conversations (preferences, biographical facts, recurring goals, etc.). Use sparingly — only for facts the user stated explicitly or that are clearly stable. For user-profile facts, prefer profile.<section>.<field> keys such as profile.preferences.food_preferences, profile.location.home_base, or profile.identity.timezone. To purge a fact, prefer memory_delete; an empty value here also deletes the key.",
		Params: []Param{
			{Name: "key", Type: "string", Description: "Short stable identifier for the fact. Use profile.<section>.<field> for visible profile facts, for example profile.preferences.food_preferences for dietary preferences. Reuse an existing key to update its value.", Required: true},
			{Name: "value", Type: "string", Description: "The fact itself, as a short human-readable string.", Required: true},
			{Name: "confidence", Type: "string", Description: "Optional confidence between 0 and 1 (default 1.0). Use values below 1.0 when the fact is inferred rather than stated.", Required: false},
		},
	}
}

func (t *MemoryWriteTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("memory store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot write memory")
	}
	key, _ := params["key"].(string)
	value, _ := params["value"].(string)
	key = store.CanonicalMemoryKey(key)
	if key == "" {
		return nil, errors.New("key is required")
	}
	confidence := parseConfidence(params["confidence"])
	if strings.TrimSpace(value) == "" {
		if err := deleteMemory(ctx, t.Store, uid, key); err != nil {
			return nil, err
		}
		return map[string]any{
			"status":  "deleted",
			"user_id": uid,
			"key":     key,
		}, nil
	}
	if err := t.Store.UpsertUserMemory(ctx, uid, key, value, confidence); err != nil {
		return nil, fmt.Errorf("upsert memory: %w", err)
	}
	return map[string]any{
		"status":     "ok",
		"user_id":    uid,
		"key":        key,
		"value":      value,
		"confidence": confidence,
	}, nil
}

// MemoryDeleteTool removes a durable fact about the current user. user_id is
// taken from context, not model-supplied parameters.
type MemoryDeleteTool struct {
	Store store.Store
}

func (t *MemoryDeleteTool) Definition() Tool {
	return Tool{
		Name:        "memory_delete",
		Description: "Deletes a single durable fact about the current user. Use this when the user asks to forget, purge, remove, clear, or correct stale state. For visible profile fields, pass the profile.<section>.<field> key when known.",
		Params: []Param{
			{Name: "key", Type: "string", Description: "Stable memory key to delete, for example next_reminder_time or profile.preferences.food_preferences.", Required: true},
		},
	}
}

func (t *MemoryDeleteTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if t.Store == nil {
		return nil, errors.New("memory store not configured")
	}
	uid := UserIDFromContext(ctx)
	if uid == "" {
		return nil, errors.New("no authenticated user on this run; cannot delete memory")
	}
	key, _ := params["key"].(string)
	key = store.CanonicalMemoryKey(key)
	if key == "" {
		return nil, errors.New("key is required")
	}
	if err := deleteMemory(ctx, t.Store, uid, key); err != nil {
		return nil, err
	}
	return map[string]any{"status": "deleted", "user_id": uid, "key": key}, nil
}

func parseConfidence(raw any) float64 {
	confidence := 1.0
	switch v := raw.(type) {
	case float64:
		confidence = v
	case string:
		if v != "" {
			var parsed float64
			if _, err := fmt.Sscanf(v, "%f", &parsed); err == nil {
				confidence = parsed
			}
		}
	}
	return confidence
}

func deleteMemory(ctx context.Context, st store.Store, userID, key string) error {
	deleter, ok := st.(memoryDeleter)
	if !ok {
		return errors.New("memory delete is not supported by the configured store")
	}
	if err := deleter.DeleteUserMemory(ctx, userID, key); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}
