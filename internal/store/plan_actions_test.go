package store

import (
	"testing"
	"time"
)

func TestPlanTodayActionsUsesLocalWeekday(t *testing.T) {
	localNow := time.Date(2026, 5, 27, 9, 0, 0, 0, time.FixedZone("local", -4*60*60))
	plan := UserPlan{
		Cadence: []PlanCadenceEntry{
			{Day: "Monday", Activity: "Easy Run + Mobility"},
			{Day: "Tuesday", Activity: "Run Intervals"},
			{Day: "Wednesday", Activity: "Recovery Ride or Swim"},
		},
		Milestones: []UserPlanMilestone{{
			Title: "Week 1 baseline build",
			Tasks: []UserPlanTask{
				{Title: "Monday: 3 easy miles plus mobility/stretching", Status: "todo"},
				{Title: "Tuesday: 6 x 400m at 1:55-2:00 with 90 sec recovery plus warmup and cooldown", Status: "todo"},
				{Title: "Wednesday: 45-60 minute easy high-cadence ride", Status: "todo"},
			},
		}},
	}

	actions := PlanTodayActions(plan, localNow)
	if len(actions) != 2 {
		t.Fatalf("expected 2 Wednesday actions, got %#v", actions)
	}
	if actions[0] != "Recovery Ride or Swim" {
		t.Fatalf("expected Wednesday cadence first, got %#v", actions)
	}
	if actions[1] != "Wednesday: 45-60 minute easy high-cadence ride" {
		t.Fatalf("expected Wednesday task, got %#v", actions)
	}
}
