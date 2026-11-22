package v1alpha1

import (
	"fmt"
	"unicode/utf8"
)

// HTTPDefaultAction is the verdict for a request that no http rule decides.
// Audit lets the request through and records it.
type HTTPDefaultAction string

const (
	HTTPDefaultAllow HTTPDefaultAction = "allow"
	HTTPDefaultDeny  HTTPDefaultAction = "deny"
	HTTPDefaultAudit HTTPDefaultAction = "audit"
)

// HTTPUnavailable is the fail posture: what happens to covered traffic while
// the gateway cannot decide. Omission means allow; the API server sets no default.
type HTTPUnavailable string

const (
	HTTPUnavailableAllow HTTPUnavailable = "allow"
	HTTPUnavailableDeny  HTTPUnavailable = "deny"
)

// HTTPSection holds the rules a gateway evaluates on decrypted requests. A deny
// outside its own except list wins, then an allow, then the default; an allow
// never cancels a deny. Rule names are unique within this section only, so a
// rule here may share its name with a rule of a kernel section.
type HTTPSection struct {
	Default       HTTPDefault     `json:"default"`
	OnUnavailable HTTPUnavailable `json:"onUnavailable,omitempty"`
	Decrypt       *HTTPDecrypt    `json:"decrypt,omitempty"`
	Rules         []HTTPRule      `json:"rules,omitempty"`
}

// HTTPDefault is complete: every traffic class requires a value.
type HTTPDefault struct {
	LLM   HTTPDefaultAction `json:"llm"`
	MCP   HTTPDefaultAction `json:"mcp"`
	Other HTTPDefaultAction `json:"other"`
}

// HTTPDecrypt lists the hosts the gateway decrypts. ExactHosts are decrypted
// as written. HostPatterns are decrypted once a matching name is seen on the
// node. Passthrough hosts are never decrypted for the selected pods.
type HTTPDecrypt struct {
	ExactHosts   []string `json:"exactHosts,omitempty"`
	HostPatterns []string `json:"hostPatterns,omitempty"`
	Passthrough  []string `json:"passthrough,omitempty"`
}

// HTTPRule has no actor: the gateway sees requests, not processes. Mode
// inherits PolicySpec.Mode when omitted; policy off still wins. The target must
// name at least one predicate: a rule that matches every request is written
// with one, for example destClass.
type HTTPRule struct {
	// Name is unique within the http section.
	Name   string          `json:"name"`
	Target HTTPTarget      `json:"target"`
	Action Action          `json:"action"`
	Except []HTTPException `json:"except,omitempty"`
	Mode   RuleMode        `json:"mode,omitempty"`
	// Origin is optional; a present empty label is distinct from omission.
	Origin *string `json:"origin,omitempty"`
}

// HTTPTarget predicates are ANDed; entries within each list are ORed.
type HTTPTarget struct {
	DestClass []string       `json:"destClass,omitempty"`
	Host      []string       `json:"host,omitempty"`
	LLM       *HTTPLLMTarget `json:"llm,omitempty"`
}

// HTTPLLMTarget matches a request to a model provider. Model applies only to
// requests that carry a model.
type HTTPLLMTarget struct {
	Provider []string `json:"provider,omitempty"`
	Region   []string `json:"region,omitempty"`
	Account  []string `json:"account,omitempty"`
	Model    []string `json:"model,omitempty"`
}

// HTTPException lifts only its containing deny and needs at least one target
// predicate: an empty exception would lift the whole deny.
type HTTPException struct {
	Target *HTTPTarget `json:"target,omitempty"`
}

func (t *HTTPTarget) hasPredicate() bool {
	if t == nil {
		return false
	}
	llm := t.LLM != nil && (len(t.LLM.Provider) > 0 || len(t.LLM.Region) > 0 || len(t.LLM.Account) > 0 || len(t.LLM.Model) > 0)
	return len(t.DestClass) > 0 || len(t.Host) > 0 || llm
}

func (e HTTPException) Validate() error {
	if !e.Target.hasPredicate() {
		return fmt.Errorf("exception requires at least one target predicate")
	}
	return e.Target.validateEntries()
}

func noEmpty(label string, entries []string) error {
	for _, entry := range entries {
		if entry == "" {
			return fmt.Errorf("%s entries must not be empty", label)
		}
	}
	return nil
}

// validateEntries rejects empty strings, which the admission schema rejects too.
func (t *HTTPTarget) validateEntries() error {
	for _, list := range []struct {
		label   string
		entries []string
	}{{"target destClass", t.DestClass}, {"target host", t.Host}} {
		if err := noEmpty(list.label, list.entries); err != nil {
			return err
		}
	}
	if t.LLM == nil {
		return nil
	}
	for _, list := range []struct {
		label   string
		entries []string
	}{{"llm provider", t.LLM.Provider}, {"llm region", t.LLM.Region}, {"llm account", t.LLM.Account}, {"llm model", t.LLM.Model}} {
		if err := noEmpty(list.label, list.entries); err != nil {
			return err
		}
	}
	return nil
}

func validHTTPDefault(a HTTPDefaultAction) bool {
	return a == HTTPDefaultAllow || a == HTTPDefaultDeny || a == HTTPDefaultAudit
}

// Validate checks the semantic constraints of the section. Like
// ValidatePolicySpec it complements the admission schema.
func (s HTTPSection) Validate() error {
	for _, d := range []struct {
		key   string
		value HTTPDefaultAction
	}{{"llm", s.Default.LLM}, {"mcp", s.Default.MCP}, {"other", s.Default.Other}} {
		if !validHTTPDefault(d.value) {
			return fmt.Errorf("http: default.%s must be allow, deny or audit", d.key)
		}
	}
	if s.OnUnavailable != "" && s.OnUnavailable != HTTPUnavailableAllow && s.OnUnavailable != HTTPUnavailableDeny {
		return fmt.Errorf("http: onUnavailable must be allow or deny when specified")
	}
	if s.Decrypt != nil {
		for _, list := range []struct {
			label   string
			entries []string
		}{{"decrypt.exactHosts", s.Decrypt.ExactHosts}, {"decrypt.hostPatterns", s.Decrypt.HostPatterns}, {"decrypt.passthrough", s.Decrypt.Passthrough}} {
			if err := noEmpty(list.label, list.entries); err != nil {
				return fmt.Errorf("http: %s", err)
			}
		}
	}
	names := map[string]bool{}
	for _, rule := range s.Rules {
		if rule.Name == "" {
			return fmt.Errorf("http: rule name must not be empty")
		}
		if utf8.RuneCountInString(rule.Name) > MaxRuleNameLength {
			return fmt.Errorf("http: rule name must be at most %d characters", MaxRuleNameLength)
		}
		if names[rule.Name] {
			return fmt.Errorf("http: rule name %q must be unique in the http section", rule.Name)
		}
		names[rule.Name] = true
		if rule.Action != ActionAllow && rule.Action != ActionDeny {
			return fmt.Errorf("http: rule %q: action must be allow or deny", rule.Name)
		}
		if rule.Mode != "" && rule.Mode != RuleAudit && rule.Mode != RuleEnforce {
			return fmt.Errorf("http: rule %q: mode must be audit or enforce when specified", rule.Name)
		}
		// Pruning turns a target made only of unsupported keys into {}, which
		// would match every request; an allow like that beats default.llm: deny.
		if !rule.Target.hasPredicate() {
			return fmt.Errorf("http: rule %q: rule target requires at least one predicate", rule.Name)
		}
		if err := rule.Target.validateEntries(); err != nil {
			return fmt.Errorf("http: rule %q: %w", rule.Name, err)
		}
		if err := validateExceptions(Rule{Name: rule.Name, Action: rule.Action}, rule.Except); err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}
	return nil
}
