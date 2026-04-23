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

// AllowlistPolicy is a Policy that enforces allowlists for filesystem paths,
// HTTP domains, and git operations, and optionally routes specific tools through
// a human-approval gate.
type AllowlistPolicy struct {
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
