package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/runner"
	"github.com/goblinsan/agent-service/internal/sse"
	"github.com/goblinsan/agent-service/internal/store"
)

// Options configures optional capabilities of an Agent.
type Options struct {
	// Runner executes tool calls. When nil, tool calls are rejected with an error.
	Runner runner.Runner
	// Policy is the base policy applied to every tool call. When nil all tool
	// calls that have a runner are allowed.
	Policy policy.Policy
	// Approvals is the approval store used for tool calls that require human
	// review. When nil, RequireApproval decisions are treated as denials.
	Approvals *policy.Approvals
}

type Agent struct {
	provider  model.Provider
	store     store.Store
	maxSteps  int
	runner    runner.Runner
	policy    policy.Policy
	approvals *policy.Approvals
}

// New returns an Agent with default options (no runner, no policy, no approvals).
func New(p model.Provider, s store.Store, maxSteps int) *Agent {
	return NewWithOptions(p, s, maxSteps, Options{})
}

// NewWithOptions returns an Agent configured with the supplied Options.
func NewWithOptions(p model.Provider, s store.Store, maxSteps int, opts Options) *Agent {
	if maxSteps <= 0 {
		maxSteps = 10
	}
	return &Agent{
		provider:  p,
		store:     s,
		maxSteps:  maxSteps,
		runner:    opts.Runner,
		policy:    opts.Policy,
		approvals: opts.Approvals,
	}
}

// Run executes the agent loop for run, streaming SSE events to w.
// runPolicy, if non-nil, overrides (or combines with) the agent's base policy
// for this specific run.
func (a *Agent) Run(ctx context.Context, run *store.Run, w http.ResponseWriter, runPolicy policy.Policy) error {
	return a.RunWithMessages(ctx, run, w, runPolicy, nil)
}

// RunWithMessages executes the agent loop using the supplied initial message
// history instead of reconstructing it from run.Prompt alone.
func (a *Agent) RunWithMessages(ctx context.Context, run *store.Run, w http.ResponseWriter, runPolicy policy.Policy, initialMessages []model.Message) error {
	// Resolve effective policy for this run.
	pol := effectivePolicy(a.policy, runPolicy)

	messages := append([]model.Message(nil), initialMessages...)
	if len(messages) == 0 {
		messages = []model.Message{
			{Role: model.RoleUser, Content: run.Prompt},
		}
	}

	// Steps are 1-based to align with human-readable step numbers in traces and SSE events.
	for i := 1; i <= a.maxSteps; i++ {
		resp, err := a.provider.Complete(ctx, model.Request{Model: run.ModelBackend, Messages: messages, MaxTokens: 512})
		if err != nil {
			return fmt.Errorf("agent step %d: %w", i, err)
		}
		run.Usage.PromptTokens += resp.Usage.PromptTokens
		run.Usage.CompletionTokens += resp.Usage.CompletionTokens
		run.Usage.TotalTokens += resp.Usage.TotalTokens

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

		// If the model requested tool calls, process them and continue the loop.
		if len(resp.ToolCalls) > 0 {
			// Add the assistant turn (with tool call requests) to the history so
			// the model has context when we feed the tool results back.
			messages = append(messages, model.Message{
				Role:      model.RoleAssistant,
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			})

			for _, tc := range resp.ToolCalls {
				result, execErr := a.executeToolCall(ctx, run, w, pol, tc)

				// Record the tool call outcome in the run record.
				rtc := store.RunToolCall{
					ID:        tc.ID,
					ToolName:  tc.Name,
					Params:    tc.Params,
					CreatedAt: time.Now().UTC(),
				}
				if execErr != nil {
					rtc.Error = execErr.Error()
				} else {
					rtc.Result = fmt.Sprintf("%v", result)
				}
				run.ToolCalls = append(run.ToolCalls, rtc)
				if err := a.store.UpdateRun(ctx, run); err != nil {
					return fmt.Errorf("persist tool call for step %d: %w", i, err)
				}

				// Feed the tool result back into the message history so that the
				// next model call has full context.
				resultContent := rtc.Result
				if execErr != nil {
					resultContent = fmt.Sprintf("error: %s", execErr)
				}
				messages = append(messages, model.Message{
					Role:       model.RoleTool,
					Content:    resultContent,
					ToolCallID: tc.ID,
				})
			}
			// Continue to the next step to let the model process tool results.
			continue
		}

		// No tool calls – check for termination.
		if resp.FinishReason == "stop" || i == a.maxSteps {
			for _, chunk := range chunkAssistantContent(resp.Content) {
				if err := sse.Write(w, sse.Event{
					Type: sse.EventRunAssistantDelta,
					Data: sse.AssistantDeltaPayload{RunID: run.ID, Delta: chunk},
				}); err != nil {
					return err
				}
			}
			run.Response = resp.Content
			break
		}

		messages = append(messages, model.Message{Role: model.RoleAssistant, Content: resp.Content})
	}

	return nil
}

// executeToolCall applies policy, handles approval gating, and then runs the
// tool via the configured runner.
func (a *Agent) executeToolCall(ctx context.Context, run *store.Run, w http.ResponseWriter, pol policy.Policy, tc model.ToolCall) (any, error) {
	// Policy evaluation.
	if pol != nil {
		decision, reason := pol.Evaluate(tc.Name, tc.Params)
		switch decision {
		case policy.Deny:
			return nil, fmt.Errorf("tool %q denied by policy: %s", tc.Name, reason)

		case policy.RequireApproval:
			status, waitErr := a.waitForApproval(ctx, run, w, tc, reason)
			if waitErr != nil {
				return nil, waitErr
			}
			if status == policy.ApprovalDenied {
				// The denial reason is already included in waitErr when denied;
				// reaching here means the context was cancelled or approval was
				// granted – only denied status returns an error from waitForApproval.
				return nil, fmt.Errorf("tool %q denied by human", tc.Name)
			}
			// Approved – fall through to execute.
		}
	}

	// Execute via the configured runner.
	if a.runner == nil {
		return nil, fmt.Errorf("tool %q requested but no runner is configured", tc.Name)
	}

	result, err := a.runner.Execute(ctx, tc.Name, tc.Params)

	// Emit a tool_call event regardless of success/failure so consumers can
	// observe every invocation.
	payload := sse.ToolCallPayload{
		RunID:    run.ID,
		ToolName: tc.Name,
		Params:   tc.Params,
	}
	if err != nil {
		payload.Error = err.Error()
	} else {
		payload.Result = fmt.Sprintf("%v", result)
	}
	_ = sse.Write(w, sse.Event{Type: sse.EventRunToolCall, Data: payload})

	return result, err
}

// waitForApproval creates an approval record, emits the appropriate SSE events,
// pauses the run, and blocks until a human approves or denies. It returns the
// final ApprovalStatus and a non-nil error if the tool was denied or the context
// was cancelled.
func (a *Agent) waitForApproval(ctx context.Context, run *store.Run, w http.ResponseWriter, tc model.ToolCall, reason string) (policy.ApprovalStatus, error) {
	if a.approvals == nil {
		// No approval store – treat RequireApproval as a denial.
		return policy.ApprovalDenied, fmt.Errorf("tool %q requires approval but no approval store is configured: %s", tc.Name, reason)
	}

	approval := a.approvals.Request(tc.Name, tc.Params)

	_ = sse.Write(w, sse.Event{
		Type: sse.EventRunApprovalRequested,
		Data: sse.ApprovalRequestedPayload{
			RunID:      run.ID,
			ApprovalID: approval.ID,
			ToolName:   tc.Name,
			Params:     tc.Params,
			Reason:     reason,
		},
	})

	// Update run status to reflect the pause.
	run.Status = "awaiting_approval"
	run.UpdatedAt = time.Now().UTC()
	_ = a.store.UpdateRun(ctx, run)
	_ = sse.Write(w, sse.Event{Type: sse.EventRunPaused, Data: run})

	// Block until the approval decision arrives or the context is cancelled.
	status, denialReason, waitErr := a.approvals.Wait(ctx, approval.ID)
	if waitErr != nil {
		return policy.ApprovalPending, fmt.Errorf("approval wait for %q: %w", tc.Name, waitErr)
	}

	// Record the approval outcome in the run and resume.
	rec := store.RunApprovalRecord{
		ApprovalID: approval.ID,
		ToolName:   tc.Name,
		Decision:   string(status),
		Reason:     denialReason,
		DecidedAt:  time.Now().UTC(),
	}
	run.ApprovalRecs = append(run.ApprovalRecs, rec)
	run.Status = "in_progress"
	run.UpdatedAt = time.Now().UTC()
	_ = a.store.UpdateRun(ctx, run)

	if status == policy.ApprovalDenied {
		return policy.ApprovalDenied, fmt.Errorf("tool %q denied by human: %s", tc.Name, denialReason)
	}
	return policy.ApprovalApproved, nil
}

// effectivePolicy merges the agent-level base policy with a per-run override.
// When both are set a CompositePolicy is returned. When only one is set that
// policy is returned directly. When neither is set nil is returned (allow all).
func effectivePolicy(base, runPolicy policy.Policy) policy.Policy {
	if base == nil && runPolicy == nil {
		return nil
	}
	if base == nil {
		return runPolicy
	}
	if runPolicy == nil {
		return base
	}
	return policy.NewCompositePolicy(base, runPolicy)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func chunkAssistantContent(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	runes := []rune(content)
	const chunkSize = 32
	chunks := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	for start := 0; start < len(runes); start += chunkSize {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}
