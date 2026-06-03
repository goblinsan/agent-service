package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

type activePlanLister interface {
	ListActivePlans(ctx context.Context, userID string) ([]store.UserPlan, error)
}

func ResolvePlanIdentityForWrite(ctx context.Context, planStore activePlanLister, userID, requestedID, title string) (string, bool, error) {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID != "" {
		if strings.TrimSpace(title) == "" {
			return requestedID, false, nil
		}
		plans, err := planStore.ListActivePlans(ctx, userID)
		if err != nil {
			return "", false, fmt.Errorf("list plans for dedupe: %w", err)
		}
		if existing := bestPlanTitleMatch(plans, title); existing != nil {
			return existing.ID, existing.ID != requestedID, nil
		}
		return requestedID, false, nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", true, nil
	}
	plans, err := planStore.ListActivePlans(ctx, userID)
	if err != nil {
		return "", false, fmt.Errorf("list plans for dedupe: %w", err)
	}
	if existing := bestPlanTitleMatch(plans, title); existing != nil {
		return existing.ID, false, nil
	}
	return "", true, nil
}

func resolvePlanIdentity(ctx context.Context, planStore store.Store, userID, requestedID, title string) (string, bool, error) {
	return ResolvePlanIdentityForWrite(ctx, planStore, userID, requestedID, title)
}

func bestPlanTitleMatch(plans []store.UserPlan, title string) *store.UserPlan {
	target := normalizePlanTitle(title)
	if target == "" {
		return nil
	}
	var best *store.UserPlan
	bestScore := -1
	for i := range plans {
		plan := &plans[i]
		if normalizePlanTitle(plan.Title) != target {
			continue
		}
		score := planRichnessScore(*plan)
		if best == nil || score > bestScore || (score == bestScore && plan.UpdatedAt.After(best.UpdatedAt)) {
			best = plan
			bestScore = score
		}
	}
	return best
}

func normalizePlanTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func planRichnessScore(plan store.UserPlan) int {
	score := 0
	if strings.TrimSpace(plan.Vision) != "" {
		score += 20
	}
	if strings.TrimSpace(plan.Target) != "" {
		score += 8
	}
	if strings.TrimSpace(plan.Category) != "" {
		score += 6
	}
	if strings.TrimSpace(plan.Summary) != "" {
		score += 4
	}
	if strings.TrimSpace(plan.ReviewCadence) != "" {
		score += 5
	}
	score += len(plan.Connectors) * 15
	score += len(plan.Objectives) * 8
	score += len(plan.Principles) * 6
	score += len(plan.TrackedMetrics) * 5
	score += len(plan.BaselineFacts) * 4
	score += len(plan.SuccessCriteria) * 4
	score += len(plan.Cadence) * 3
	score += len(plan.SupportingSections) * 6
	score += len(plan.Tags)
	score += len(plan.DataSources) * 2
	score += len(plan.Milestones) * 20
	for _, milestone := range plan.Milestones {
		score += len(milestone.Tasks) * 2
	}
	score += len(plan.Steps)
	score += len(plan.Metrics)
	return score
}
