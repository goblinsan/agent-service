package store

import (
	"testing"
	"time"
)

func TestBuildUserProfileFallsBackToLegacyLocation(t *testing.T) {
	updatedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	profile := BuildUserProfile("u1", []UserMemory{
		{Key: "location", Value: "Example City, Example State", UpdatedAt: updatedAt},
	})

	location := findProfileSection(t, profile, "location")
	homeBase := findProfileField(t, location, "home_base")
	if homeBase.Value != "Example City, Example State" {
		t.Fatalf("expected legacy location to populate home_base, got %q", homeBase.Value)
	}
	if homeBase.UpdatedAt == nil || !homeBase.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("expected legacy updated_at to be preserved, got %#v", homeBase.UpdatedAt)
	}
}

func TestBuildUserProfileFallsBackToLegacyDietaryPreference(t *testing.T) {
	updatedAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	profile := BuildUserProfile("u1", []UserMemory{
		{Key: "dietary_preference", Value: "vegetarian", UpdatedAt: updatedAt},
	})

	preferences := findProfileSection(t, profile, "preferences")
	foodPreferences := findProfileField(t, preferences, "food_preferences")
	if foodPreferences.Value != "vegetarian" {
		t.Fatalf("expected legacy dietary_preference to populate food_preferences, got %q", foodPreferences.Value)
	}
	if foodPreferences.UpdatedAt == nil || !foodPreferences.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("expected legacy updated_at to be preserved, got %#v", foodPreferences.UpdatedAt)
	}
}

func TestCanonicalMemoryKeyMapsLocationToProfileHomeBase(t *testing.T) {
	got := CanonicalMemoryKey("location")
	want := "profile.location.home_base"
	if got != want {
		t.Fatalf("CanonicalMemoryKey(location) = %q, want %q", got, want)
	}
}

func TestCanonicalMemoryKeyMapsDietaryPreferenceToProfileFoodPreferences(t *testing.T) {
	for _, key := range []string{"diet", "dietary_preference", "dietary_preferences", "food_preference", "food_preferences"} {
		got := CanonicalMemoryKey(key)
		want := "profile.preferences.food_preferences"
		if got != want {
			t.Fatalf("CanonicalMemoryKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func findProfileSection(t *testing.T, profile UserProfile, id string) UserProfileSection {
	t.Helper()
	for _, section := range profile.Sections {
		if section.ID == id {
			return section
		}
	}
	t.Fatalf("profile section %q not found", id)
	return UserProfileSection{}
}

func findProfileField(t *testing.T, section UserProfileSection, key string) UserProfileField {
	t.Helper()
	for _, field := range section.Fields {
		if field.Key == key {
			return field
		}
	}
	t.Fatalf("profile field %q not found", key)
	return UserProfileField{}
}
