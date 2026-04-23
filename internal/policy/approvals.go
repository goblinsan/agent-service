package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ApprovalStatus represents the current state of an approval request.
type ApprovalStatus string

const (
	// ApprovalPending means the request has not yet been decided.
	ApprovalPending ApprovalStatus = "pending"
	// ApprovalApproved means a human approved the tool call.
	ApprovalApproved ApprovalStatus = "approved"
	// ApprovalDenied means a human denied the tool call.
	ApprovalDenied ApprovalStatus = "denied"
)

// Approval holds the details and current status of a pending tool-call approval.
type Approval struct {
	ID        string         `json:"id"`
	ToolName  string         `json:"tool_name"`
	Params    map[string]any `json:"params"`
	Status    ApprovalStatus `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`

	// ch is closed when a decision (approve or deny) is made.
	ch chan struct{}
}

// Approvals is an in-memory store for pending tool-call approvals.
// It is safe for concurrent use.
type Approvals struct {
	mu      sync.RWMutex
	records map[string]*Approval
}

// NewApprovals returns an empty Approvals store.
func NewApprovals() *Approvals {
	return &Approvals{records: make(map[string]*Approval)}
}

// Request creates a new pending approval for the given tool call and returns
// the newly created Approval record.
func (a *Approvals) Request(toolName string, params map[string]any) *Approval {
	id := newApprovalID()
	now := time.Now().UTC()
	rec := &Approval{
		ID:        id,
		ToolName:  toolName,
		Params:    params,
		Status:    ApprovalPending,
		CreatedAt: now,
		UpdatedAt: now,
		ch:        make(chan struct{}),
	}
	a.mu.Lock()
	a.records[id] = rec
	a.mu.Unlock()
	return rec
}

// Approve marks the approval as approved and unblocks any callers of Wait.
func (a *Approvals) Approve(id string) error {
	return a.decide(id, ApprovalApproved, "")
}

// Deny marks the approval as denied with the given reason and unblocks any
// callers of Wait.
func (a *Approvals) Deny(id, reason string) error {
	return a.decide(id, ApprovalDenied, reason)
}

// Get returns the Approval with the given ID, or an error if not found.
func (a *Approvals) Get(id string) (*Approval, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	rec, ok := a.records[id]
	if !ok {
		return nil, fmt.Errorf("approval %q not found", id)
	}
	return rec, nil
}

// Wait blocks until the approval with the given ID has been decided or ctx is
// cancelled. It returns the final status and an optional reason.
func (a *Approvals) Wait(ctx context.Context, id string) (ApprovalStatus, string, error) {
	rec, err := a.Get(id)
	if err != nil {
		return ApprovalPending, "", err
	}
	select {
	case <-rec.ch:
		a.mu.RLock()
		status, reason := rec.Status, rec.Reason
		a.mu.RUnlock()
		return status, reason, nil
	case <-ctx.Done():
		return ApprovalPending, "", ctx.Err()
	}
}

// decide updates the record to status/reason and closes the notification channel.
func (a *Approvals) decide(id string, status ApprovalStatus, reason string) error {
	a.mu.Lock()
	rec, ok := a.records[id]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("approval %q not found", id)
	}
	if rec.Status != ApprovalPending {
		a.mu.Unlock()
		return fmt.Errorf("approval %q already decided (%s)", id, rec.Status)
	}
	rec.Status = status
	rec.Reason = reason
	rec.UpdatedAt = time.Now().UTC()
	ch := rec.ch
	a.mu.Unlock()
	close(ch)
	return nil
}

func newApprovalID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
