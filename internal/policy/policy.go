package policy

import (
	"net/url"
	"strings"
)

// Decision is the outcome of evaluating a policy rule for a tool call.
type Decision int

const (
	// Allow permits the tool call to proceed.
	Allow Decision = iota
	// Deny blocks the tool call.
	Deny
	// RequireApproval pauses execution until a human approves or denies the call.
	RequireApproval
)

// String returns a human-readable representation of d.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case RequireApproval:
		return "require_approval"
	default:
		return "unknown"
	}
}

// Policy evaluates tool calls and returns an access decision.
type Policy interface {
	// Evaluate returns the decision for the given tool call and an optional reason.
	Evaluate(toolName string, params map[string]any) (Decision, string)
}

// AllowlistPolicy is a Policy that enforces allowlists for tool names,
// filesystem paths, HTTP domains, and git operations, and optionally routes
// specific tools through a human-approval gate.
type AllowlistPolicy struct {
	// AllowedToolNames, when non-empty, limits which tool names the agent may
	// call. Any tool not in this list is denied.
	AllowedToolNames []string

	// DeniedToolNames lists tools that are completely blocked regardless of
	// other rules.
	DeniedToolNames []string

	// AllowedPaths is the list of path prefixes the file tool may access.
	// When empty, no additional path restriction is applied.
	AllowedPaths []string

	// AllowedDomains is the list of hostnames the http tool may contact.
	// Sub-domains are automatically permitted (e.g. "example.com" also covers
	// "api.example.com"). When empty, no domain restriction is applied.
	AllowedDomains []string

	// AllowedGitOps is the list of git operations (e.g. "git_status") that may
	// run. When empty, all git operations are allowed.
	AllowedGitOps []string

	// ApprovalTools is the set of tool names that require human approval before
	// each execution.
	ApprovalTools map[string]bool
}

// Evaluate checks the tool call against the configured allowlists and approval
// requirements.
func (p *AllowlistPolicy) Evaluate(toolName string, params map[string]any) (Decision, string) {
	// Explicit deny list takes highest priority.
	for _, d := range p.DeniedToolNames {
		if d == toolName {
			return Deny, "tool is denied by policy"
		}
	}

	// If an allowed-tool-names allowlist is configured, only listed tools pass.
	if len(p.AllowedToolNames) > 0 {
		found := false
		for _, a := range p.AllowedToolNames {
			if a == toolName {
				found = true
				break
			}
		}
		if !found {
			return Deny, "tool is not in the allowed tools list"
		}
	}

	// Approval gate check.
	if p.ApprovalTools[toolName] {
		return RequireApproval, "tool requires human approval"
	}

	switch toolName {
	case "file":
		return p.evaluateFile(params)
	case "http":
		return p.evaluateHTTP(params)
	case "git":
		return p.evaluateGit(params)
	}

	return Allow, ""
}

func (p *AllowlistPolicy) evaluateFile(params map[string]any) (Decision, string) {
	if len(p.AllowedPaths) == 0 {
		return Allow, ""
	}
	path, _ := params["path"].(string)
	for _, prefix := range p.AllowedPaths {
		if strings.HasPrefix(path, prefix) {
			return Allow, ""
		}
	}
	return Deny, "path is not in the allowed filesystem paths"
}

func (p *AllowlistPolicy) evaluateHTTP(params map[string]any) (Decision, string) {
	if len(p.AllowedDomains) == 0 {
		return Allow, ""
	}
	rawURL, _ := params["url"].(string)
	u, err := url.Parse(rawURL)
	if err != nil {
		return Deny, "invalid URL"
	}
	host := u.Hostname()
	for _, d := range p.AllowedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return Allow, ""
		}
	}
	return Deny, "domain is not in the allowed domains list"
}

func (p *AllowlistPolicy) evaluateGit(params map[string]any) (Decision, string) {
	if len(p.AllowedGitOps) == 0 {
		return Allow, ""
	}
	op, _ := params["op"].(string)
	for _, allowed := range p.AllowedGitOps {
		if op == allowed {
			return Allow, ""
		}
	}
	return Deny, "git operation is not in the allowed operations list"
}

// CompositePolicy chains multiple policies and returns the most restrictive
// outcome: Deny takes precedence over RequireApproval, which takes precedence
// over Allow.
type CompositePolicy struct {
	policies []Policy
}

// NewCompositePolicy returns a CompositePolicy that evaluates all supplied
// policies and returns the strictest decision.
func NewCompositePolicy(policies ...Policy) *CompositePolicy {
	return &CompositePolicy{policies: policies}
}

// Evaluate runs every contained policy. The first Deny short-circuits. A
// RequireApproval from any policy upgrades the result but does not stop
// evaluation (a later Deny can still override it).
func (c *CompositePolicy) Evaluate(toolName string, params map[string]any) (Decision, string) {
	result := Allow
	var reason string
	for _, p := range c.policies {
		if p == nil {
			continue
		}
		d, r := p.Evaluate(toolName, params)
		switch d {
		case Deny:
			return Deny, r
		case RequireApproval:
			if result == Allow {
				result = RequireApproval
				reason = r
			}
		}
	}
	return result, reason
}
