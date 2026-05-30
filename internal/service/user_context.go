package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/goblinsan/agent-service/internal/model"
	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/goblinsan/agent-service/internal/store"
)

// userMemoryEventLimit caps how many recent events are summarised back into
// each model turn. Older events stay in the database; only the most recent are
// surfaced in the system context window.
const userMemoryEventLimit = 8

var personalContextToolNames = []string{
	"memory_recall",
	"memory_write",
	"memory_delete",
	"plan_list",
	"plan_upsert",
	"plan_ingest_text",
	"create_project_checkin",
	"get_daily_rollup",
	"get_unmapped_records",
	"apple_health_ingest_activity",
	"apple_health_ingest_nutrition",
	"healthfit_ingest_summary",
	"loseit_ingest_summary",
}

var goalsContextToolNames = []string{
	"plan_list",
	"plan_upsert",
	"plan_ingest_text",
	"create_project_checkin",
}

var memoryContextToolNames = []string{
	"memory_recall",
	"memory_write",
	"memory_delete",
}

var personalDataContextToolNames = []string{
	"get_daily_rollup",
	"get_unmapped_records",
	"apple_health_ingest_activity",
	"apple_health_ingest_nutrition",
	"healthfit_ingest_summary",
	"loseit_ingest_summary",
}

type personalContextPolicy struct {
	Enabled      bool
	Profile      bool
	Memories     bool
	Goals        bool
	Events       bool
	PersonalData bool
}

func defaultPersonalContextPolicy() personalContextPolicy {
	return personalContextPolicy{
		Enabled:      true,
		Profile:      true,
		Memories:     true,
		Goals:        true,
		Events:       true,
		PersonalData: true,
	}
}

func personalContextPolicyForAgent(agent AgentConfig, ok bool) personalContextPolicy {
	result := defaultPersonalContextPolicy()
	if !ok {
		return result
	}
	configured := agent.PersonalContext
	if configured.Enabled != nil {
		result.Enabled = *configured.Enabled
	}
	if !result.Enabled {
		result.Profile = false
		result.Memories = false
		result.Goals = false
		result.Events = false
		result.PersonalData = false
		return result
	}
	if configured.Profile != nil {
		result.Profile = *configured.Profile
	}
	if configured.Memories != nil {
		result.Memories = *configured.Memories
	}
	if configured.Goals != nil {
		result.Goals = *configured.Goals
	}
	if configured.Events != nil {
		result.Events = *configured.Events
	}
	if configured.PersonalData != nil {
		result.PersonalData = *configured.PersonalData
	}
	return result
}

func personalContextToolPolicy(config personalContextPolicy) policy.Policy {
	if config.Enabled && config.Profile && config.Memories && config.Goals && config.PersonalData {
		return nil
	}
	denied := map[string]bool{}
	if !config.Enabled {
		for _, name := range personalContextToolNames {
			denied[name] = true
		}
	} else {
		if !config.Profile || !config.Memories {
			for _, name := range memoryContextToolNames {
				denied[name] = true
			}
		}
		if !config.Goals {
			for _, name := range goalsContextToolNames {
				denied[name] = true
			}
		}
		if !config.PersonalData {
			for _, name := range personalDataContextToolNames {
				denied[name] = true
			}
		}
	}
	if len(denied) == 0 {
		return nil
	}
	deniedNames := make([]string, 0, len(denied))
	for name := range denied {
		deniedNames = append(deniedNames, name)
	}
	sort.Strings(deniedNames)
	return &policy.AllowlistPolicy{DeniedToolNames: deniedNames}
}

func combinePolicies(policies ...policy.Policy) policy.Policy {
	filtered := make([]policy.Policy, 0, len(policies))
	for _, p := range policies {
		if p != nil {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return policy.NewCompositePolicy(filtered...)
}

// ensureUserAndContext registers the user (if new) and returns a system message
// summarising what agent-service knows about them. The returned slice is empty
// when there is no userID, no persisted memory, and no recent events.
func (s *Service) ensureUserAndContext(ctx context.Context, userID string, contextPolicy personalContextPolicy) []model.Message {
	if s == nil || s.store == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	if contextPolicy.Enabled && (contextPolicy.Profile || contextPolicy.Memories || contextPolicy.Goals || contextPolicy.Events || contextPolicy.PersonalData) {
		if err := s.store.EnsureUser(ctx, userID, ""); err != nil {
			slog.Warn("ensure user failed", "user_id", userID, "err", err)
		}
	}
	var memories []store.UserMemory
	if contextPolicy.Profile || contextPolicy.Memories {
		var err error
		memories, err = s.store.ListUserMemories(ctx, userID)
		if err != nil {
			slog.Warn("list user memories failed", "user_id", userID, "err", err)
		}
	}
	var events []store.UserEvent
	if contextPolicy.Events {
		var err error
		events, err = s.store.ListRecentUserEvents(ctx, userID, userMemoryEventLimit)
		if err != nil {
			slog.Warn("list user events failed", "user_id", userID, "err", err)
		}
	}
	var plans []store.UserPlan
	if contextPolicy.Goals {
		var err error
		plans, err = s.store.ListActivePlans(ctx, userID)
		if err != nil {
			slog.Warn("list user plans failed", "user_id", userID, "err", err)
		}
	}

	var b strings.Builder
	localNow := appendCurrentTimeContext(&b, time.Now(), resolveServiceLocation(s.localTimezone))
	rollups := s.listContextDailyRollups(ctx, userID, localNow, contextPolicy)
	b.WriteString("Your training data is stale. For weather, rain, temperature, wind, forecast, or whether outdoor plans are advisable, MUST call weather_forecast; if the user does not specify a location, ask a short follow-up when no location is known. For ANY other question about current events, recent scores, standings, prices, releases, news, or anything that could have changed in the last year (including questions phrased as \"this year\", \"latest\", \"current\", \"who won\", \"how much is\"), you MUST call web_search with a query that uses the actual current year shown above. Do NOT call web_search with a year from your training data. Cite the URL returned by web_search. Do not answer such questions from memory.\n\n")

	if !contextPolicy.Enabled {
		b.WriteString("This agent is configured without personal context. Do not use or infer the account owner's profile, durable memories, goals, plans, personal-data rollups, or recent events. Interact only with the current conversation unless the user explicitly supplies information in this chat. Do not call memory, plan, or personal-data tools.")
		return []model.Message{{Role: model.RoleSystem, Content: b.String()}}
	}

	if contextPolicy.Profile {
		profile := store.BuildUserProfile(userID, memories)
		b.WriteString("User profile:\n")
		profileFacts := 0
		for _, section := range profile.Sections {
			fields := make([]string, 0, len(section.Fields))
			for _, field := range section.Fields {
				if strings.TrimSpace(field.Value) == "" {
					continue
				}
				fields = append(fields, fmt.Sprintf("%s: %s", field.Label, field.Value))
			}
			if len(fields) == 0 {
				continue
			}
			profileFacts += len(fields)
			fmt.Fprintf(&b, "- %s: %s\n", section.Title, strings.Join(fields, "; "))
		}
		if profileFacts == 0 {
			b.WriteString("- (no structured profile facts recorded yet)\n")
		}
	}

	if contextPolicy.Memories {
		fmt.Fprintf(&b, "\nKnown facts about user %q:\n", userID)
		if len(memories) == 0 {
			b.WriteString("- (no durable facts recorded yet)\n")
		} else {
			written := 0
			for _, m := range memories {
				if store.IsProfileMemoryKey(m.Key) {
					continue
				}
				fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
				written++
			}
			if written == 0 {
				b.WriteString("- (no other durable facts recorded yet)\n")
			}
		}
	}
	if contextPolicy.Goals && len(plans) > 0 {
		b.WriteString("\nActive plans:\n")
		for _, p := range plans {
			summary := p.Summary
			if summary == "" {
				summary = p.Status
			}
			fmt.Fprintf(&b, "- [%s] %s", p.Status, p.Title)
			if p.Target != "" {
				fmt.Fprintf(&b, " (target: %s)", p.Target)
			}
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
			if len(p.Connectors) > 0 {
				fmt.Fprintf(&b, " [connectors: %s]", formatPlanConnectors(p.Connectors))
			}
			fmt.Fprintf(&b, " — %s\n", summary)
			if p.Progress.TaskCount > 0 {
				fmt.Fprintf(&b, "  - progress: %.0f%% (%d/%d tasks complete)\n", p.Progress.PercentComplete, p.Progress.CompletedTasks, p.Progress.TaskCount)
			}
			if todayActions := store.PlanTodayActions(p, localNow); len(todayActions) > 0 {
				fmt.Fprintf(&b, "  - actions for today (%s %s): %s\n", localNow.Weekday().String(), localNow.Format("2006-01-02"), strings.Join(todayActions, "; "))
			} else if nextOpen := store.PlanNextOpenTasks(p, 3); len(nextOpen) > 0 {
				fmt.Fprintf(&b, "  - next open tasks: %s\n", strings.Join(nextOpen, "; "))
			}
			if len(p.Milestones) > 0 {
				for i, milestone := range p.Milestones {
					if i >= 3 {
						fmt.Fprintf(&b, "  - ... %d more milestone(s)\n", len(p.Milestones)-i)
						break
					}
					milestoneStatus := strings.TrimSpace(milestone.Status)
					if milestoneStatus == "" {
						milestoneStatus = "todo"
					}
					fmt.Fprintf(&b, "  - milestone (%s): %s\n", milestoneStatus, milestone.Title)
					if len(milestone.Tasks) > 12 {
						fmt.Fprintf(&b, "    - %d task(s)\n", len(milestone.Tasks))
					} else {
						for j, task := range milestone.Tasks {
							if j >= 2 {
								fmt.Fprintf(&b, "    - ... %d more task(s)\n", len(milestone.Tasks)-j)
								break
							}
							taskStatus := strings.TrimSpace(task.Status)
							if taskStatus == "" {
								taskStatus = "todo"
							}
							fmt.Fprintf(&b, "    - (%s) %s\n", taskStatus, task.Title)
						}
					}
				}
			} else {
				if len(p.Steps) > 12 {
					fmt.Fprintf(&b, "  - %d step(s)\n", len(p.Steps))
				} else {
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
				}
			}
			if len(p.Metrics) > 0 {
				fmt.Fprintf(&b, "  - metrics: %v\n", p.Metrics)
			}
			if len(p.Objectives) > 0 {
				fmt.Fprintf(&b, "  - objectives: %s\n", strings.Join(p.Objectives, "; "))
			}
			if len(p.Principles) > 0 {
				fmt.Fprintf(&b, "  - principles: %s\n", strings.Join(p.Principles, "; "))
			}
			if len(p.BaselineFacts) > 0 {
				facts := make([]string, 0, len(p.BaselineFacts))
				for _, fact := range p.BaselineFacts {
					if strings.TrimSpace(fact.Label) == "" || strings.TrimSpace(fact.Value) == "" {
						continue
					}
					facts = append(facts, fmt.Sprintf("%s=%s", fact.Label, fact.Value))
				}
				if len(facts) > 0 {
					fmt.Fprintf(&b, "  - baselines: %s\n", strings.Join(facts, "; "))
				}
			}
			if len(p.TrackedMetrics) > 0 {
				names := make([]string, 0, len(p.TrackedMetrics))
				for _, metric := range p.TrackedMetrics {
					if strings.TrimSpace(metric.Name) != "" {
						names = append(names, metric.Name)
					}
				}
				if len(names) > 0 {
					fmt.Fprintf(&b, "  - tracked metrics: %s\n", strings.Join(names, "; "))
				}
			}
			if len(p.SuccessCriteria) > 0 {
				fmt.Fprintf(&b, "  - success criteria: %s\n", strings.Join(p.SuccessCriteria, "; "))
			}
			if len(p.Cadence) > 0 {
				entries := make([]string, 0, len(p.Cadence))
				for _, entry := range p.Cadence {
					if strings.TrimSpace(entry.Activity) == "" {
						continue
					}
					prefix := firstNonEmptyPlanString(entry.Day, entry.Label, "session")
					entries = append(entries, fmt.Sprintf("%s=%s", prefix, entry.Activity))
				}
				if len(entries) > 0 {
					fmt.Fprintf(&b, "  - cadence: %s\n", strings.Join(entries, "; "))
				}
			}
		}
	}
	if contextPolicy.PersonalData {
		appendDailyRollupContext(&b, rollups, localNow)
	}
	if contextPolicy.Events && len(events) > 0 {
		b.WriteString("\nRecent events (newest first):\n")
		for _, e := range events {
			ts := e.CreatedAt.UTC().Format(time.RFC3339)
			fmt.Fprintf(&b, "- %s [%s] %s\n", ts, e.Kind, e.Summary)
		}
	}
	if contextPolicy.Profile || contextPolicy.Memories || contextPolicy.Events {
		b.WriteString("\nUse the structured user profile and durable facts when relevant. If the user states a new durable preference or biographical fact, call memory_write using profile.<section>.<field> keys for profile-shaped facts when appropriate. If the user asks to forget, purge, remove, clear, or correct stale durable facts, call memory_delete for the affected keys instead of writing empty or placeholder values. Append significant turns to the event log via memory tools where appropriate.")
	}
	if contextPolicy.Goals {
		b.WriteString("\nGoals and plans: when the user states a new goal, commitment, or multi-step intent (e.g. \"I want to...\", \"help me track...\", \"my plan is...\"), call plan_upsert to record it. Call plan_list before creating a new plan to avoid duplicates and to recall existing plan ids. When the user reports progress on an existing plan, update it via plan_upsert with the same id and a revised steps array. Mark plans 'done' when finished or 'abandoned' when dropped.")
		b.WriteString("\nWhen the user asks for project-manager-style guidance, planning help, or a goal review, call plan_list early in the run so you are using the latest durable plan state instead of stale assumptions. For day-specific guidance, trust the local_weekday/local_date above and the plan_list today_actions field over any inferred weekday from model memory.")
		b.WriteString("\nWhen the user pastes a plan, roadmap, or text document from another system and wants it tracked, call plan_ingest_text to convert that text into a durable plan before giving follow-up guidance.")
	}
	if contextPolicy.PersonalData {
		b.WriteString("\nWhen the user shares personal data app updates (for example Apple Health, Strava, or nutrition trackers), call plan_ingest_text with connector/connectors metadata so health and nutrition goals stay current in durable plans.")
		b.WriteString("\nPrefer Apple Health as the aggregate source when activity, workout, body, or nutrition data is available there, including nutrition values written by apps such as Lose It. For structured Apple Health activity/workout/body payloads, call apple_health_ingest_activity. For structured Apple Health nutrition payloads, call apple_health_ingest_nutrition. For direct HealthFit payloads, call healthfit_ingest_summary. For direct Lose It payloads, call loseit_ingest_summary. These tools update durable plans and append recent events so project-manager check-ins can reference current health and nutrition progress.")
	}
	b.WriteString("\nReminders and delayed follow-ups: when the user asks to be reminded later or wants something to happen at a future time, call create_schedule. Prefer delay_seconds for relative times like 'in 1 minute'. Do not use memory_write as a substitute for reminders.")
	b.WriteString("\nWhen the user asks whether a reminder or check-in already exists, or asks what is scheduled tomorrow morning / later today, call list_schedules before creating anything new. list_schedules returns active reminders only; call list_schedule_history only when the user explicitly asks for past reminders or reminder history.")
	if contextPolicy.Goals {
		b.WriteString("\nRecurring project-manager follow-ups: when the user wants regular morning, afternoon, or night check-ins about goals, progress, and next actions, call create_project_checkin instead of create_schedule.")
	}
	return []model.Message{{Role: model.RoleSystem, Content: b.String()}}
}

func (s *Service) listContextDailyRollups(ctx context.Context, userID string, localNow time.Time, contextPolicy personalContextPolicy) []store.DailyRollup {
	if !contextPolicy.Enabled || !contextPolicy.PersonalData {
		return nil
	}
	rollupStore, ok := s.store.(store.DailyRollupStore)
	if !ok {
		return nil
	}
	today := dateOnlyUTC(localNow)
	start := today.AddDate(0, 0, -1)
	end := today
	rollups, err := rollupStore.ListDailyRollups(ctx, userID, start, end)
	if err != nil {
		slog.Warn("list daily rollups failed", "user_id", userID, "err", err)
		return nil
	}
	if len(rollups) > 2 {
		return rollups[:2]
	}
	return rollups
}

// prependUserContext returns the given messages with a user-context system
// message inserted after any leading persona system prompt(s), or at the very
// start when there is no leading system prompt.
func (s *Service) prependUserContext(ctx context.Context, userID string, msgs []model.Message, contextPolicy personalContextPolicy) []model.Message {
	ctxMsgs := s.ensureUserAndContext(ctx, userID, contextPolicy)
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

func appendCurrentTimeContext(b *strings.Builder, now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	utcNow := now.UTC()
	localNow := utcNow.In(loc)
	fmt.Fprintf(b, "Authoritative user-local date/time: %s at %s (timezone: %s; abbreviation: %s; UTC offset: %s). If the user asks what day, date, or time it is, answer from this user-local line.\n", localNow.Format("Monday, January 2, 2006"), localNow.Format("15:04:05 MST"), loc.String(), localNow.Format("MST"), localNow.Format("-07:00"))
	fmt.Fprintf(b, "Machine-readable user-local time: local_date=%s; local_weekday=%s; local_time=%s; local_timezone=%s; local_timezone_abbreviation=%s; local_utc_offset=%s. Use local_date/local_weekday for today/tomorrow and plan actions.\n", localNow.Format("2006-01-02"), localNow.Weekday().String(), localNow.Format("15:04:05"), loc.String(), localNow.Format("MST"), localNow.Format("-07:00"))
	fmt.Fprintf(b, "UTC reference only, not the user's local day unless explicitly requested: utc_datetime=%s; utc_weekday=%s.\n", utcNow.Format(time.RFC3339), utcNow.Weekday().String())
	return localNow
}

func appendDailyRollupContext(b *strings.Builder, rollups []store.DailyRollup, localNow time.Time) {
	if len(rollups) == 0 {
		return
	}
	today := dateOnlyUTC(localNow)
	yesterday := today.AddDate(0, 0, -1)
	b.WriteString("\nPersonal data rollups:\n")
	for _, rollup := range rollups {
		label := rollup.Date.Format("2006-01-02")
		switch dateOnlyUTC(rollup.Date) {
		case today:
			label = "today"
		case yesterday:
			label = "yesterday"
		}
		summary := strings.TrimSpace(rollup.SummaryText)
		if summary == "" {
			summary = "structured rollup available"
		}
		fmt.Fprintf(b, "- %s (%s): %s", label, rollup.Date.Format("2006-01-02"), summary)
		if len(rollup.SourceSystemsIncluded) > 0 {
			fmt.Fprintf(b, " [sources: %s]", strings.Join(rollup.SourceSystemsIncluded, ", "))
		}
		b.WriteString("\n")
		if totals := formatRollupTotals(rollup.StructuredRollup); totals != "" {
			fmt.Fprintf(b, "  - totals: %s\n", totals)
		}
		if lookahead := strings.TrimSpace(rollup.LookaheadText); lookahead != "" {
			fmt.Fprintf(b, "  - lookahead: %s\n", lookahead)
		}
		if unresolved := formatUnmappedRollupCount(rollup.UnmappedRecordsSummary); unresolved != "" {
			fmt.Fprintf(b, "  - unresolved: %s\n", unresolved)
		}
	}
}

func formatRollupTotals(structured map[string]any) string {
	if structured == nil {
		return ""
	}
	totals, ok := structured["totals"].(map[string]any)
	if !ok || len(totals) == 0 {
		return ""
	}
	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		total, ok := totals[key].(map[string]any)
		if !ok {
			continue
		}
		value, ok := total["value"].(float64)
		if !ok {
			continue
		}
		unit, _ := total["unit"].(string)
		parts = append(parts, strings.TrimSpace(fmt.Sprintf("%s=%.2f %s", key, value, unit)))
		if len(parts) >= 8 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func formatUnmappedRollupCount(unmapped map[string]any) string {
	if unmapped == nil {
		return ""
	}
	count, ok := unmapped["count"].(float64)
	if !ok || count <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f unmapped accepted records", count)
}

func dateOnlyUTC(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func formatPlanConnectors(connectors []store.PlanConnector) string {
	parts := make([]string, 0, len(connectors))
	for _, c := range connectors {
		app := strings.TrimSpace(c.App)
		if app == "" {
			continue
		}
		domain := strings.TrimSpace(c.Domain)
		typ := strings.TrimSpace(c.Type)
		switch {
		case domain != "":
			parts = append(parts, fmt.Sprintf("%s:%s", app, domain))
		case typ != "":
			parts = append(parts, fmt.Sprintf("%s:%s", app, typ))
		default:
			parts = append(parts, app)
		}
	}
	return strings.Join(parts, ", ")
}

func firstNonEmptyPlanString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolveServiceLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
