package policy_test

import (
	"testing"

	"github.com/goblinsan/agent-service/internal/policy"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// AllowlistPolicy – file tool
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_File_EmptyAllowedPaths(t *testing.T) {
	p := &policy.AllowlistPolicy{}
	d, _ := p.Evaluate("file", map[string]any{"op": "read_file", "path": "/etc/passwd"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_File_AllowedPath(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedPaths: []string{"/workspace/"}}
	d, _ := p.Evaluate("file", map[string]any{"op": "read_file", "path": "/workspace/notes.txt"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_File_DeniedPath(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedPaths: []string{"/workspace/"}}
	d, reason := p.Evaluate("file", map[string]any{"op": "read_file", "path": "/etc/passwd"})
	assert.Equal(t, policy.Deny, d)
	assert.NotEmpty(t, reason)
}

func TestAllowlistPolicy_File_MultiplePrefixes(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedPaths: []string{"/workspace/", "/tmp/"}}
	d, _ := p.Evaluate("file", map[string]any{"op": "write_file", "path": "/tmp/out.txt"})
	assert.Equal(t, policy.Allow, d)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – http tool
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_HTTP_EmptyAllowedDomains(t *testing.T) {
	p := &policy.AllowlistPolicy{}
	d, _ := p.Evaluate("http", map[string]any{"url": "https://evil.com/data"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_HTTP_AllowedDomain(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedDomains: []string{"example.com"}}
	d, _ := p.Evaluate("http", map[string]any{"url": "https://example.com/api"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_HTTP_AllowedSubdomain(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedDomains: []string{"example.com"}}
	d, _ := p.Evaluate("http", map[string]any{"url": "https://api.example.com/v1"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_HTTP_DeniedDomain(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedDomains: []string{"example.com"}}
	d, reason := p.Evaluate("http", map[string]any{"url": "https://evil.com/data"})
	assert.Equal(t, policy.Deny, d)
	assert.NotEmpty(t, reason)
}

func TestAllowlistPolicy_HTTP_InvalidURL(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedDomains: []string{"example.com"}}
	d, _ := p.Evaluate("http", map[string]any{"url": "://bad url"})
	assert.Equal(t, policy.Deny, d)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – git tool
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_Git_EmptyAllowedOps(t *testing.T) {
	p := &policy.AllowlistPolicy{}
	d, _ := p.Evaluate("git", map[string]any{"op": "git_status"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_Git_AllowedOp(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedGitOps: []string{"git_status", "git_log"}}
	d, _ := p.Evaluate("git", map[string]any{"op": "git_log"})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_Git_DeniedOp(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedGitOps: []string{"git_status", "git_log"}}
	d, reason := p.Evaluate("git", map[string]any{"op": "git_diff"})
	assert.Equal(t, policy.Deny, d)
	assert.NotEmpty(t, reason)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – approval gate
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_RequiresApproval(t *testing.T) {
	p := &policy.AllowlistPolicy{
		ApprovalTools: map[string]bool{"file": true},
	}
	d, reason := p.Evaluate("file", map[string]any{"op": "write_file", "path": "/workspace/x"})
	assert.Equal(t, policy.RequireApproval, d)
	assert.NotEmpty(t, reason)
}

func TestAllowlistPolicy_ApprovalGateTakesPrecedence(t *testing.T) {
	// Even when AllowedPaths is configured, the approval gate fires first.
	p := &policy.AllowlistPolicy{
		AllowedPaths:  []string{"/workspace/"},
		ApprovalTools: map[string]bool{"file": true},
	}
	d, _ := p.Evaluate("file", map[string]any{"op": "read_file", "path": "/workspace/ok"})
	assert.Equal(t, policy.RequireApproval, d)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – unknown tool
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_UnknownTool_Allow(t *testing.T) {
	p := &policy.AllowlistPolicy{}
	d, _ := p.Evaluate("artifact", map[string]any{"op": "save_artifact"})
	assert.Equal(t, policy.Allow, d)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – AllowedToolNames
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_AllowedToolNames_PermitsListed(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedToolNames: []string{"echo", "search"}}
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_AllowedToolNames_DeniesUnlisted(t *testing.T) {
	p := &policy.AllowlistPolicy{AllowedToolNames: []string{"echo"}}
	d, reason := p.Evaluate("file", map[string]any{})
	assert.Equal(t, policy.Deny, d)
	assert.NotEmpty(t, reason)
}

func TestAllowlistPolicy_AllowedToolNames_Empty_AllowsAll(t *testing.T) {
	p := &policy.AllowlistPolicy{}
	d, _ := p.Evaluate("anything", map[string]any{})
	assert.Equal(t, policy.Allow, d)
}

// ---------------------------------------------------------------------------
// AllowlistPolicy – DeniedToolNames
// ---------------------------------------------------------------------------

func TestAllowlistPolicy_DeniedToolNames_BlocksDenied(t *testing.T) {
	p := &policy.AllowlistPolicy{DeniedToolNames: []string{"rm", "shutdown"}}
	d, reason := p.Evaluate("rm", map[string]any{})
	assert.Equal(t, policy.Deny, d)
	assert.NotEmpty(t, reason)
}

func TestAllowlistPolicy_DeniedToolNames_AllowsOthers(t *testing.T) {
	p := &policy.AllowlistPolicy{DeniedToolNames: []string{"rm"}}
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Allow, d)
}

func TestAllowlistPolicy_DeniedToolNames_TakesPrecedenceOverAllowed(t *testing.T) {
	// A tool is in both the allow-list and the deny-list; deny wins.
	p := &policy.AllowlistPolicy{
		AllowedToolNames: []string{"echo"},
		DeniedToolNames:  []string{"echo"},
	}
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Deny, d)
}

// ---------------------------------------------------------------------------
// CompositePolicy
// ---------------------------------------------------------------------------

func TestCompositePolicy_AllAllow(t *testing.T) {
	p := policy.NewCompositePolicy(
		&policy.AllowlistPolicy{},
		&policy.AllowlistPolicy{},
	)
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Allow, d)
}

func TestCompositePolicy_AnyDenyWins(t *testing.T) {
	p := policy.NewCompositePolicy(
		&policy.AllowlistPolicy{},
		&policy.AllowlistPolicy{DeniedToolNames: []string{"echo"}},
	)
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Deny, d)
}

func TestCompositePolicy_RequireApprovalElevatesDecision(t *testing.T) {
	p := policy.NewCompositePolicy(
		&policy.AllowlistPolicy{},
		&policy.AllowlistPolicy{ApprovalTools: map[string]bool{"echo": true}},
	)
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.RequireApproval, d)
}

func TestCompositePolicy_DenyOverridesRequireApproval(t *testing.T) {
	// Even when one policy says RequireApproval, a Deny from another wins.
	p := policy.NewCompositePolicy(
		&policy.AllowlistPolicy{ApprovalTools: map[string]bool{"echo": true}},
		&policy.AllowlistPolicy{DeniedToolNames: []string{"echo"}},
	)
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Deny, d)
}

func TestCompositePolicy_NilPoliciesIgnored(t *testing.T) {
	p := policy.NewCompositePolicy(nil, nil)
	d, _ := p.Evaluate("echo", map[string]any{})
	assert.Equal(t, policy.Allow, d)
}

// ---------------------------------------------------------------------------
// Decision.String
// ---------------------------------------------------------------------------

func TestDecision_String(t *testing.T) {
	assert.Equal(t, "allow", policy.Allow.String())
	assert.Equal(t, "deny", policy.Deny.String())
	assert.Equal(t, "require_approval", policy.RequireApproval.String())
}
