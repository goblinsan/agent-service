package store

import (
	"strings"
	"time"
)

const profileMemoryPrefix = "profile."

type profileFieldSpec struct {
	Key   string
	Label string
}

type profileSectionSpec struct {
	ID     string
	Title  string
	Fields []profileFieldSpec
}

var defaultProfileSections = []profileSectionSpec{
	{ID: "identity", Title: "Identity", Fields: []profileFieldSpec{
		{Key: "display_name", Label: "Display name"},
		{Key: "age", Label: "Age"},
		{Key: "pronouns", Label: "Pronouns"},
		{Key: "timezone", Label: "Timezone"},
	}},
	{ID: "location", Title: "Location", Fields: []profileFieldSpec{
		{Key: "home_base", Label: "Home base"},
		{Key: "region", Label: "Region"},
		{Key: "travel_context", Label: "Travel context"},
	}},
	{ID: "family", Title: "Family", Fields: []profileFieldSpec{
		{Key: "household", Label: "Household"},
		{Key: "partner", Label: "Partner"},
		{Key: "children", Label: "Children"},
		{Key: "family_notes", Label: "Family notes"},
	}},
	{ID: "preferences", Title: "Preferences", Fields: []profileFieldSpec{
		{Key: "communication_style", Label: "Communication style"},
		{Key: "coaching_style", Label: "Coaching style"},
		{Key: "decision_style", Label: "Decision style"},
		{Key: "food_preferences", Label: "Food preferences"},
	}},
	{ID: "health", Title: "Health", Fields: []profileFieldSpec{
		{Key: "training_context", Label: "Training context"},
		{Key: "health_goals", Label: "Health goals"},
		{Key: "constraints", Label: "Constraints"},
	}},
	{ID: "work", Title: "Work", Fields: []profileFieldSpec{
		{Key: "current_focus", Label: "Current focus"},
		{Key: "projects", Label: "Projects"},
		{Key: "constraints", Label: "Constraints"},
	}},
}

func ProfileMemoryKey(sectionID, fieldKey string) string {
	return profileMemoryPrefix + sanitizeProfileKey(sectionID) + "." + sanitizeProfileKey(fieldKey)
}

func IsProfileMemoryKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), profileMemoryPrefix)
}

func BuildUserProfile(userID string, memories []UserMemory) UserProfile {
	byKey := make(map[string]UserMemory, len(memories))
	legacyByKey := make(map[string]UserMemory, len(memories))
	var latest *time.Time
	for _, memory := range memories {
		key := strings.ToLower(strings.TrimSpace(memory.Key))
		if key == "" {
			continue
		}
		if IsProfileMemoryKey(key) {
			byKey[key] = memory
		} else {
			legacyByKey[key] = memory
		}
		updatedAt := memory.UpdatedAt
		if latest == nil || updatedAt.After(*latest) {
			latest = &updatedAt
		}
	}

	sections := make([]UserProfileSection, 0, len(defaultProfileSections))
	for _, sectionSpec := range defaultProfileSections {
		section := UserProfileSection{ID: sectionSpec.ID, Title: sectionSpec.Title}
		for _, fieldSpec := range sectionSpec.Fields {
			memoryKey := ProfileMemoryKey(sectionSpec.ID, fieldSpec.Key)
			field := UserProfileField{Key: fieldSpec.Key, Label: fieldSpec.Label}
			if memory, ok := byKey[memoryKey]; ok {
				field.Value = memory.Value
				updatedAt := memory.UpdatedAt
				field.UpdatedAt = &updatedAt
			}
			if strings.TrimSpace(field.Value) == "" {
				if memory, ok := legacyByKey[LegacyProfileMemoryKey(sectionSpec.ID, fieldSpec.Key)]; ok {
					field.Value = memory.Value
					updatedAt := memory.UpdatedAt
					field.UpdatedAt = &updatedAt
				}
			}
			section.Fields = append(section.Fields, field)
		}
		sections = append(sections, section)
	}

	return UserProfile{UserID: userID, Sections: sections, UpdatedAt: latest}
}

func CanonicalMemoryKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "location", "home_base", "home", "city":
		return ProfileMemoryKey("location", "home_base")
	case "timezone", "time_zone":
		return ProfileMemoryKey("identity", "timezone")
	case "diet", "dietary_preference", "dietary_preferences", "food_preference", "food_preferences", "nutrition_preference", "nutrition_preferences":
		return ProfileMemoryKey("preferences", "food_preferences")
	default:
		return key
	}
}

func LegacyProfileMemoryKey(sectionID, fieldKey string) string {
	switch sanitizeProfileKey(sectionID) + "." + sanitizeProfileKey(fieldKey) {
	case "location.home_base":
		return "location"
	case "identity.timezone":
		return "timezone"
	case "preferences.food_preferences":
		return "dietary_preference"
	default:
		return ""
	}
}

func sanitizeProfileKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
