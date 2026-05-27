package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
)

// userMemoryEventLimit caps how many recent events are summarised back into
// each model turn. Older events stay in the database; only the most recent are
// surfaced in the system context window.
const userMemoryEventLimit = 8

// ensureUserAndContext registers the user (if new) and returns a system message
// summarising what agent-service knows about them. The returned slice is empty
// when there is no userID, no persisted memory, and no recent events.
func (s *Service) ensureUserAndContext(ctx context.Context, userID string) []model.Message {
	if s == nil || s.store == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	if err := s.store.EnsureUser(ctx, userID, ""); err != nil {
		slog.Warn("ensure user failed", "user_id", userID, "err", err)
	}
	memories, err := s.store.ListUserMemories(ctx, userID)
	if err != nil {
		slog.Warn("list user memories failed", "user_id", userID, "err", err)
	}
	events, err := s.store.ListRecentUserEvents(ctx, userID, userMemoryEventLimit)
	if err != nil {
		slog.Warn("list user events failed", "user_id", userID, "err", err)
	}
	plans, err := s.store.ListActivePlans(ctx, userID)
	if err != nil {
		slog.Warn("list user plans failed", "user_id", userID, "err", err)
	}

	var b strings.Builder
	now := time.Now().UTC()
	fmt.Fprintf(&b, "Current UTC date/time: %s (today is %s).\n", now.Format(time.RFC3339), now.Format("Monday, January 2, 2006"))
	b.WriteString("Your training data is stale. For ANY question about current events, recent scores, standings, prices, releases, news, weather, or anything that could have changed in the last year (including questions phrased as \"this year\", \"latest\", \"current\", \"who won\", \"how much is\"), you MUST call web_search with a query that uses the actual current year shown above. Do NOT call web_search with a year from your training data. Cite the URL returned by web_search. Do not answer such questions from memory.\n\n")

	fmt.Fprintf(&b, "Known facts about user %q:\n", userID)
	if len(memories) == 0 {
		b.WriteString("- (no durable facts recorded yet)\n")
	} else {
		for _, m := range memories {
			fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
		}
	}
	if len(plans) > 0 {
		b.WriteString("\nActive plans:\n")
		for _, p := range plans {
			summary := p.Summary
			if summary == "" {
				summary = p.Status
			}
			fmt.Fprintf(&b, "- [%s] %s", p.Status, p.Title)
			if p.Category != "" {
				fmt.Fprintf(&b, " <%s>", p.Category)
			}
			if p.ReviewCadence != "" {
				fmt.Fprintf(&b, " [review: %s]", p.ReviewCadence)
			}
			if len(p.Tags) > 0 {
				fmt.Fprintf(&b, " [tags: %s]", strings.Join(p.Tags, ", "))
			}
			if len(p.DataSources) > 0 {
				fmt.Fprintf(&b, " [sources: %s]", strings.Join(p.DataSources, ", "))
			}
			fmt.Fprintf(&b, " — %s\n", summary)
			for i, step := range p.Steps {
				if i >= 5 {
					fmt.Fprintf(&b, "  - ... %d more step(s)\n", len(p.Steps)-i)
					break
				}
				stepTitle, _ := step["title"].(string)
				stepStatus, _ := step["status"].(string)
				if strings.TrimSpace(stepTitle) == "" {
					continue
				}
				if stepStatus == "" {
					stepStatus = "todo"
				}
				fmt.Fprintf(&b, "  - (%s) %s\n", stepStatus, stepTitle)
			}
			if len(p.Metrics) > 0 {
				fmt.Fprintf(&b, "  - metrics: %v\n", p.Metrics)
			}
		}
	}
	if len(events) > 0 {
		b.WriteString("\nRecent events (newest first):\n")
		for _, e := range events {
			ts := e.CreatedAt.UTC().Format(time.RFC3339)
			fmt.Fprintf(&b, "- %s [%s] %s\n", ts, e.Kind, e.Summary)
		}
	}
	b.WriteString("\nUse these facts and events when relevant. If the user states a new durable preference or biographical fact, call memory_write. Append significant turns to the event log via memory tools where appropriate.")
	b.WriteString("\nGoals and plans: when the user states a new goal, commitment, or multi-step intent (e.g. \"I want to...\", \"help me track...\", \"my plan is...\"), call plan_upsert to record it. Call plan_list before creating a new plan to avoid duplicates and to recall existing plan ids. When the user reports progress on an existing plan, update it via plan_upsert with the same id and a revised steps array. Mark plans 'done' when finished or 'abandoned' when dropped.")
	b.WriteString("\nWhen the user asks for project-manager-style guidance, planning help, or a goal review, call plan_list early in the run so you are using the latest durable plan state instead of stale assumptions.")
	b.WriteString("\nWhen the user pastes a plan, roadmap, or text document from another system and wants it tracked, call plan_ingest_text to convert that text into a durable plan before giving follow-up guidance.")
	b.WriteString("\nReminders and delayed follow-ups: when the user asks to be reminded later or wants something to happen at a future time, call create_schedule. Prefer delay_seconds for relative times like 'in 1 minute'. Do not use memory_write as a substitute for reminders.")
	b.WriteString("\nRecurring project-manager follow-ups: when the user wants regular morning, afternoon, or night check-ins about goals, progress, and next actions, call create_project_checkin instead of create_schedule.")
	return []model.Message{{Role: model.RoleSystem, Content: b.String()}}
}

// prependUserContext returns the given messages with a user-context system
// message inserted after any leading persona system prompt(s), or at the very
// start when there is no leading system prompt.
func (s *Service) prependUserContext(ctx context.Context, userID string, msgs []model.Message) []model.Message {
	ctxMsgs := s.ensureUserAndContext(ctx, userID)
	if len(ctxMsgs) == 0 {
		return msgs
	}
	insertAt := 0
	for i, m := range msgs {
		if m.Role == model.RoleSystem {
			insertAt = i + 1
			continue
		}
		break
	}
	out := make([]model.Message, 0, len(msgs)+len(ctxMsgs))
	out = append(out, msgs[:insertAt]...)
	out = append(out, ctxMsgs...)
	out = append(out, msgs[insertAt:]...)
	return out
}
