package service

import (
	"context"
	"strings"

	"github.com/goblinsan/agent-service/internal/store"
)

func (s *Service) GetUserProfile(ctx context.Context, userID string) (*store.UserProfile, error) {
	userID = strings.TrimSpace(userID)
	if err := s.store.EnsureUser(ctx, userID, ""); err != nil {
		return nil, err
	}
	memories, err := s.store.ListUserMemories(ctx, userID)
	if err != nil {
		return nil, err
	}
	profile := store.BuildUserProfile(userID, memories)
	return &profile, nil
}

func (s *Service) UpsertUserProfile(ctx context.Context, userID string, profile *store.UserProfile) (*store.UserProfile, error) {
	userID = strings.TrimSpace(userID)
	if err := s.store.EnsureUser(ctx, userID, ""); err != nil {
		return nil, err
	}
	if profile != nil {
		for _, section := range profile.Sections {
			sectionID := strings.TrimSpace(section.ID)
			if sectionID == "" {
				continue
			}
			for _, field := range section.Fields {
				fieldKey := strings.TrimSpace(field.Key)
				if fieldKey == "" {
					continue
				}
				memoryKey := store.ProfileMemoryKey(sectionID, fieldKey)
				if err := s.store.UpsertUserMemory(ctx, userID, memoryKey, field.Value, 1.0); err != nil {
					return nil, err
				}
			}
		}
	}
	return s.GetUserProfile(ctx, userID)
}
