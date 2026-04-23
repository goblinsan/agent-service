package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovals_Request(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("file", map[string]any{"op": "write_file", "path": "/tmp/x"})

	assert.NotEmpty(t, rec.ID)
	assert.Equal(t, "file", rec.ToolName)
	assert.Equal(t, policy.ApprovalPending, rec.Status)
	assert.False(t, rec.CreatedAt.IsZero())
	assert.False(t, rec.UpdatedAt.IsZero())
}

func TestApprovals_Get(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("http", map[string]any{"url": "https://example.com"})

	got, err := a.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestApprovals_GetNotFound(t *testing.T) {
	a := policy.NewApprovals()
	_, err := a.Get("nonexistent-id")
	require.Error(t, err)
}

func TestApprovals_Approve(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("file", map[string]any{"op": "read_file", "path": "/tmp/x"})

	require.NoError(t, a.Approve(rec.ID))

	got, err := a.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, policy.ApprovalApproved, got.Status)
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestApprovals_Deny(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("git", map[string]any{"op": "git_diff"})

	require.NoError(t, a.Deny(rec.ID, "not allowed during freeze"))

	got, err := a.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, policy.ApprovalDenied, got.Status)
	assert.Equal(t, "not allowed during freeze", got.Reason)
}

func TestApprovals_ApproveNotFound(t *testing.T) {
	a := policy.NewApprovals()
	err := a.Approve("no-such-id")
	require.Error(t, err)
}

func TestApprovals_DenyNotFound(t *testing.T) {
	a := policy.NewApprovals()
	err := a.Deny("no-such-id", "reason")
	require.Error(t, err)
}

func TestApprovals_AlreadyDecided(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("file", map[string]any{})

	require.NoError(t, a.Approve(rec.ID))
	// Attempting to decide again must fail.
	err := a.Deny(rec.ID, "too late")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already decided")
}

func TestApprovals_Wait_Approve(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("file", map[string]any{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = a.Approve(rec.ID)
	}()

	status, reason, err := a.Wait(context.Background(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, policy.ApprovalApproved, status)
	assert.Empty(t, reason)
}

func TestApprovals_Wait_Deny(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("http", map[string]any{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = a.Deny(rec.ID, "policy violation")
	}()

	status, reason, err := a.Wait(context.Background(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, policy.ApprovalDenied, status)
	assert.Equal(t, "policy violation", reason)
}

func TestApprovals_Wait_ContextCancelled(t *testing.T) {
	a := policy.NewApprovals()
	rec := a.Request("file", map[string]any{})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	status, _, err := a.Wait(ctx, rec.ID)
	require.Error(t, err)
	assert.Equal(t, policy.ApprovalPending, status)
}

func TestApprovals_Wait_NotFound(t *testing.T) {
	a := policy.NewApprovals()
	status, _, err := a.Wait(context.Background(), "missing")
	require.Error(t, err)
	assert.Equal(t, policy.ApprovalPending, status)
}

func TestApprovals_IDs_AreUnique(t *testing.T) {
	a := policy.NewApprovals()
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		rec := a.Request("tool", map[string]any{})
		assert.False(t, ids[rec.ID], "duplicate ID: %s", rec.ID)
		ids[rec.ID] = true
	}
}
