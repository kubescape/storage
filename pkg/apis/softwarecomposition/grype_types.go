package softwarecomposition

import (
	"encoding/json"
)

type Cvss struct {
	Version        string
	Vector         string
	Metrics        CvssMetrics
	VendorMetadata json.RawMessage
}

type CvssMetrics struct {
	BaseScore           float64
	ExploitabilityScore *float64
	ImpactScore         *float64
}

type VulnerabilityMetadata struct {
	ID          string
	DataSource  string
	Namespace   string
	Severity    string
	URLs        []string
	Description string
	Cvss        []Cvss
}

type Vulnerability struct {
	VulnerabilityMetadata
	Fix        Fix
	Advisories []Advisory
}

type Fix struct {
	Versions []string
	State    string
}

type Advisory struct {
	ID   string
	Link string
}

type SyftType string
type SyftLanguage string
type MetadataType string

type SyftCoordinates struct {
	RealPath     string
	FileSystemID string
}

type GrypePackage struct {
	Name         string
	Version      string
	Type         SyftType
	Locations    []SyftCoordinates
	Language     SyftLanguage
	Licenses     []string
	CPEs         []string
	PURL         string
	Upstreams    []UpstreamPackage
	MetadataType MetadataType
	Metadata     json.RawMessage
}

type UpstreamPackage struct {
	Name    string
	Version string
}

type Match struct {
	Vulnerability          Vulnerability
	RelatedVulnerabilities []VulnerabilityMetadata
	MatchDetails           []MatchDetails
	Artifact               GrypePackage
}

type MatchDetails struct {
	Type       string
	Matcher    string
	SearchedBy json.RawMessage
	Found      json.RawMessage
}

type IgnoredMatch struct {
	Match
	AppliedIgnoreRules []IgnoreRule
}

type IgnoreRule struct {
	Vulnerability string
	FixState      string
	Package       *IgnoreRulePackage

	// SourceKind, SourceName and SourceNamespace identify the object that
	// caused this suppression (e.g. "SecurityException", its name, and
	// namespace for a namespaced object).
	SourceKind      string
	SourceName      string
	SourceNamespace string
	// Justification and ImpactStatement carry the stated reason for the
	// suppression, in VEX vocabulary.
	Justification   string
	ImpactStatement string
}

type IgnoreRulePackage struct {
	Name         string
	Version      string
	Language     string
	Type         string
	Location     string
	UpstreamName string
}

type Distribution struct {
	Name    string
	Version string
	IDLike  []string
}

type Descriptor struct {
	Name                  string
	Version               string
	Configuration         json.RawMessage
	VulnerabilityDBStatus json.RawMessage
}

type Source struct {
	Type   string
	Target json.RawMessage
}

// GrypeDocument is the document that represents the vulnerability manifest in
// the Grype’s JSON format
type GrypeDocument struct {
	Matches        []Match
	IgnoredMatches []IgnoredMatch
	Source         *Source
	Distro         Distribution
	Descriptor_    Descriptor
}
