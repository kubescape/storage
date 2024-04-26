package v1beta1

import (
	"encoding/json"
)

type Cvss struct {
	Version        string          `json:"version"`
	Vector         string          `json:"vector"`
	Metrics        CvssMetrics     `json:"metrics"`
	VendorMetadata json.RawMessage `json:"vendorMetadata,omitempty"`
}

type CvssMetrics struct {
	BaseScore           float64  `json:"baseScore"`
	ExploitabilityScore *float64 `json:"exploitabilityScore,omitempty"`
	ImpactScore         *float64 `json:"impactScore,omitempty"`
}

type VulnerabilityMetadata struct {
	ID          string   `json:"id"`
	DataSource  string   `json:"dataSource"`
	Namespace   string   `json:"namespace,omitempty"`
	Severity    string   `json:"severity,omitempty"`
	URLs        []string `json:"urls"`
	Description string   `json:"description,omitempty"`
	Cvss        []Cvss   `json:"cvss"`
}

type Vulnerability struct {
	VulnerabilityMetadata
	Fix        Fix        `json:"fix"`
	Advisories []Advisory `json:"advisories"`
}

type Fix struct {
	Versions []string `json:"versions"`
	State    string   `json:"state"`
}

type Advisory struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

type SyftType string
type SyftLanguage string
type MetadataType string

type SyftCoordinates struct {
	RealPath     string `json:"path"`
	FileSystemID string `json:"layerID,omitempty"`
}

type GrypePackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Type         SyftType          `json:"type"`
	Locations    []SyftCoordinates `json:"locations"`
	Language     SyftLanguage      `json:"language"`
	Licenses     []string          `json:"licenses"`
	CPEs         []string          `json:"cpes"`
	PURL         string            `json:"purl"`
	Upstreams    []UpstreamPackage `json:"upstreams"`
	MetadataType MetadataType      `json:"metadataType,omitempty"`
	Metadata     json.RawMessage   `json:"metadata,omitempty"`
}

type UpstreamPackage struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Match struct {
	Vulnerability          Vulnerability           `json:"vulnerability"`
	RelatedVulnerabilities []VulnerabilityMetadata `json:"relatedVulnerabilities"`
	MatchDetails           []MatchDetails          `json:"matchDetails"`
	Artifact               GrypePackage            `json:"artifact"`
}

type MatchDetails struct {
	Type       string          `json:"type"`
	Matcher    string          `json:"matcher"`
	SearchedBy json.RawMessage `json:"searchedBy,omitempty"`
	Found      json.RawMessage `json:"found,omitempty"`
}

type IgnoredMatch struct {
	Match
	AppliedIgnoreRules []IgnoreRule `json:"appliedIgnoreRules"`
}

type IgnoreRule struct {
	Vulnerability string             `json:"vulnerability,omitempty"`
	FixState      string             `json:"fix-state,omitempty"`
	Package       *IgnoreRulePackage `json:"package,omitempty"`
}

type IgnoreRulePackage struct {
	Name         string `json:"name,omitempty"`
	Version      string `json:"version,omitempty"`
	Type         string `json:"type,omitempty"`
	Location     string `json:"location,omitempty"`
	UpstreamName string `json:"upstream-name,omitempty"`
}

type Distribution struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	IDLike  []string `json:"idLike"`
}

type Descriptor struct {
	Name                  string          `json:"name"`
	Version               string          `json:"version"`
	Configuration         json.RawMessage `json:"configuration,omitempty"`
	VulnerabilityDBStatus json.RawMessage `json:"db,omitempty"`
}

type Source struct {
	Type   string          `json:"type"`
	Target json.RawMessage `json:"target,omitempty"`
}

// GrypeDocument is the document that represents the vulnerability manifest in
// the Grype’s JSON format
type GrypeDocument struct {
	Matches        []Match        `json:"matches"`
	IgnoredMatches []IgnoredMatch `json:"ignoredMatches,omitempty"`
	Source         *Source        `json:"source"`
	Distro         Distribution   `json:"distro"`
	Descriptor     Descriptor     `json:"descriptor"`
}
