package store

import (
	"strconv"
	"strings"
	"time"
)

func NormalizeUserPlan(plan *UserPlan) {
	if plan == nil {
		return
	}
	if plan.Objectives == nil {
		plan.Objectives = []string{}
	}
	if plan.Principles == nil {
		plan.Principles = []string{}
	}
	if plan.BaselineFacts == nil {
		plan.BaselineFacts = []PlanFact{}
	}
	if plan.TrackedMetrics == nil {
		plan.TrackedMetrics = []PlanTrackedMetric{}
	}
	if plan.SuccessCriteria == nil {
		plan.SuccessCriteria = []string{}
	}
	if plan.Cadence == nil {
		plan.Cadence = []PlanCadenceEntry{}
	}
	if plan.SupportingSections == nil {
		plan.SupportingSections = []PlanSupportingSection{}
	}
	if len(plan.Milestones) == 0 && len(plan.Steps) > 0 {
		plan.Milestones = []UserPlanMilestone{{
			ID:    "m1",
			Title: "Plan tasks",
			Tasks: stepsToTasks(plan.Steps),
		}}
	}
	if len(plan.Steps) == 0 && len(plan.Milestones) > 0 {
		plan.Steps = milestonesToSteps(plan.Milestones)
	}
	plan.Progress = computePlanProgress(*plan)
}

func computePlanProgress(plan UserPlan) UserPlanProgress {
	progress := UserPlanProgress{
		MilestoneCount: len(plan.Milestones),
		IsStale:        !plan.UpdatedAt.IsZero() && time.Since(plan.UpdatedAt.UTC()) > 14*24*time.Hour,
	}
	for _, milestone := range plan.Milestones {
		if milestoneDone(milestone) {
			progress.CompletedMilestones++
		}
		progress.TaskCount += len(milestone.Tasks)
		for _, task := range milestone.Tasks {
			if taskDone(task.Status) {
				progress.CompletedTasks++
			}
		}
	}
	if progress.TaskCount == 0 && len(plan.Steps) > 0 {
		progress.TaskCount = len(plan.Steps)
		for _, step := range plan.Steps {
			status, _ := step["status"].(string)
			if taskDone(status) {
				progress.CompletedTasks++
			}
		}
	}
	switch {
	case progress.TaskCount > 0:
		progress.PercentComplete = float64(progress.CompletedTasks) / float64(progress.TaskCount) * 100
	case progress.MilestoneCount > 0:
		progress.PercentComplete = float64(progress.CompletedMilestones) / float64(progress.MilestoneCount) * 100
	}
	if next, ok := nextReviewAt(plan.ReviewCadence, plan.UpdatedAt); ok {
		progress.NextReviewAt = &next
	}
	return progress
}

func nextReviewAt(cadence string, base time.Time) (time.Time, bool) {
	cadence = strings.ToLower(strings.TrimSpace(cadence))
	if cadence == "" {
		return time.Time{}, false
	}
	if base.IsZero() {
		base = time.Now().UTC()
	}
	switch {
	case strings.Contains(cadence, "day"):
		return base.Add(24 * time.Hour), true
	case strings.Contains(cadence, "week"):
		return base.Add(7 * 24 * time.Hour), true
	case strings.Contains(cadence, "month"):
		return base.Add(30 * 24 * time.Hour), true
	case strings.Contains(cadence, "quarter"):
		return base.Add(90 * 24 * time.Hour), true
	default:
		return time.Time{}, false
	}
}

func milestoneDone(m UserPlanMilestone) bool {
	if taskDone(m.Status) {
		return true
	}
	if len(m.Tasks) == 0 {
		return false
	}
	for _, t := range m.Tasks {
		if !taskDone(t.Status) {
			return false
		}
	}
	return true
}

func taskDone(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "done")
}

func milestonesToSteps(milestones []UserPlanMilestone) []map[string]any {
	steps := make([]map[string]any, 0, 8)
	for _, milestone := range milestones {
		for _, task := range milestone.Tasks {
			title := strings.TrimSpace(task.Title)
			if title == "" {
				continue
			}
			steps = append(steps, map[string]any{
				"title":  title,
				"status": normalizePlanStatus(task.Status, "todo"),
			})
		}
	}
	return steps
}

func stepsToTasks(steps []map[string]any) []UserPlanTask {
	tasks := make([]UserPlanTask, 0, len(steps))
	for i, step := range steps {
		title, _ := step["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		status, _ := step["status"].(string)
		tasks = append(tasks, UserPlanTask{
			ID:     "t" + strconv.Itoa(i+1),
			Title:  title,
			Status: normalizePlanStatus(status, "todo"),
		})
	}
	return tasks
}

func normalizePlanStatus(status, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "todo", "doing", "done", "blocked", "paused", "active":
		return normalized
	default:
		return fallback
	}
}
