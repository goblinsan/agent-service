package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

type Agent struct {
	provider model.Provider
	store    store.Store
	maxSteps int
}

func New(p model.Provider, s store.Store, maxSteps int) *Agent {
	if maxSteps <= 0 {
		maxSteps = 10
	}
	return &Agent{provider: p, store: s, maxSteps: maxSteps}
}

func (a *Agent) Run(ctx context.Context, run *store.Run, w http.ResponseWriter) error {
	messages := []model.Message{
		{Role: model.RoleUser, Content: run.Prompt},
	}

	// Steps are 1-based to align with human-readable step numbers in traces and SSE events.
	for i := 1; i <= a.maxSteps; i++ {
		resp, err := a.provider.Complete(ctx, model.Request{Messages: messages, MaxTokens: 512})
		if err != nil {
			return fmt.Errorf("agent step %d: %w", i, err)
		}

		step := &store.RunStep{
			ID:        newID(),
			RunID:     run.ID,
			Index:     i,
			Role:      string(model.RoleAssistant),
			Content:   resp.Content,
			CreatedAt: time.Now().UTC(),
		}
		if err := a.store.CreateStep(ctx, step); err != nil {
			return fmt.Errorf("persist step %d: %w", i, err)
		}

		if err := sse.Write(w, sse.Event{Type: sse.EventRunStep, Data: step}); err != nil {
			return err
		}

		if resp.FinishReason == "stop" || i == a.maxSteps {
			break
		}

		messages = append(messages, model.Message{Role: model.RoleAssistant, Content: resp.Content})
	}

	return nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
