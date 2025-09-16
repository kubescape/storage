package v1beta1

import (
	"encoding/json"
)

type Cvss struct {
	Version        string          `json:"version" protobuf:"bytes,1,req,name=version"`
	Vector         string          `json:"vector" protobuf:"bytes,2,req,name=vector"`
	Metrics        CvssMetrics     `json:"metrics" protobuf:"bytes,3,req,name=metrics"`
	VendorMetadata json.RawMessage `json:"vendorMetadata,omitempty" protobuf:"bytes,4,opt,name=vendorMetadata"`
}

type CvssMetrics struct {
	BaseScore           float64  `json:"baseScore" protobuf:"bytes,1,req,name=baseScore"`
	ExploitabilityScore *float64 `json:"exploitabilityScore,omitempty" protobuf:"bytes,2,opt,name=exploitabilityScore"`
	ImpactScore         *float64 `json:"impactScore,omitempty" protobuf:"bytes,3,opt,name=impactScore"`
}

type VulnerabilityMetadata struct {
	ID          string   `json:"id" protobuf:"bytes,1,req,name=id"`
	DataSource  string   `json:"dataSource" protobuf:"bytes,2,req,name=dataSource"`
	Namespace   string   `json:"namespace,omitempty" protobuf:"bytes,3,opt,name=namespace"`
	Severity    string   `json:"severity,omitempty" protobuf:"bytes,4,opt,name=severity"`
	URLs        []string `json:"urls" protobuf:"bytes,5,rep,name=urls"`
	Description string   `json:"description,omitempty" protobuf:"bytes,6,opt,name=description"`
	Cvss        []Cvss   `json:"cvss" protobuf:"bytes,7,rep,name=cvss"`
}

type Vulnerability struct {
	VulnerabilityMetadata `protobuf:"bytes,8,opt,name=vulnerabilityMetadata"`
	Fix                   Fix        `json:"fix" protobuf:"bytes,9,req,name=fix"`
	Advisories            []Advisory `json:"advisories" protobuf:"bytes,10,rep,name=advisories"`
}

type Fix struct {
	Versions []string `json:"versions" protobuf:"bytes,1,rep,name=versions"`
	State    string   `json:"state" protobuf:"bytes,2,req,name=state"`
}

type Advisory struct {
	ID   string `json:"id" protobuf:"bytes,1,req,name=id"`
	Link string `json:"link" protobuf:"bytes,2,req,name=link"`
}

type SyftType string
type SyftLanguage string
type MetadataType string

type SyftCoordinates struct {
	RealPath     string `json:"path" protobuf:"bytes,1,req,name=path"`
	FileSystemID string `json:"layerID,omitempty" protobuf:"bytes,2,opt,name=layerID"`
}

type GrypePackage struct {
	Name         string            `json:"name" protobuf:"bytes,1,req,name=name"`
	Version      string            `json:"version" protobuf:"bytes,2,req,name=version"`
	Type         SyftType          `json:"type" protobuf:"bytes,3,req,name=type"`
	Locations    []SyftCoordinates `json:"locations" protobuf:"bytes,4,rep,name=locations"`
	Language     SyftLanguage      `json:"language" protobuf:"bytes,5,req,name=language"`
	Licenses     []string          `json:"licenses" protobuf:"bytes,6,rep,name=licenses"`
	CPEs         []string          `json:"cpes" protobuf:"bytes,7,rep,name=cpes"`
	PURL         string            `json:"purl" protobuf:"bytes,8,req,name=purl"`
	Upstreams    []UpstreamPackage `json:"upstreams" protobuf:"bytes,9,rep,name=upstreams"`
	MetadataType MetadataType      `json:"metadataType,omitempty" protobuf:"bytes,10,opt,name=metadataType"`
	Metadata     json.RawMessage   `json:"metadata,omitempty" protobuf:"bytes,11,opt,name=metadata"`
}

type UpstreamPackage struct {
	Name    string `json:"name" protobuf:"bytes,1,req,name=name"`
	Version string `json:"version,omitempty" protobuf:"bytes,2,opt,name=version"`
}

type Match struct {
	Vulnerability          Vulnerability           `json:"vulnerability" protobuf:"bytes,1,req,name=vulnerability"`
	RelatedVulnerabilities []VulnerabilityMetadata `json:"relatedVulnerabilities" protobuf:"bytes,2,rep,name=relatedVulnerabilities"`
	MatchDetails           []MatchDetails          `json:"matchDetails" protobuf:"bytes,3,rep,name=matchDetails"`
	Artifact               GrypePackage            `json:"artifact" protobuf:"bytes,4,req,name=artifact"`
}

type MatchDetails struct {
	Type       string          `json:"type" protobuf:"bytes,1,req,name=type"`
	Matcher    string          `json:"matcher" protobuf:"bytes,2,req,name=matcher"`
	SearchedBy json.RawMessage `json:"searchedBy,omitempty" protobuf:"bytes,3,opt,name=searchedBy"`
	Found      json.RawMessage `json:"found,omitempty" protobuf:"bytes,4,opt,name=found"`
}

type IgnoredMatch struct {
	Match              `protobuf:"bytes,1,opt,name=match"`
	AppliedIgnoreRules []IgnoreRule `json:"appliedIgnoreRules" protobuf:"bytes,2,rep,name=appliedIgnoreRules"`
}

type IgnoreRule struct {
	Vulnerability string             `json:"vulnerability,omitempty" protobuf:"bytes,1,opt,name=vulnerability"`
	FixState      string             `json:"fix-state,omitempty" protobuf:"bytes,2,opt,name=fixstate"`
	Package       *IgnoreRulePackage `json:"package,omitempty" protobuf:"bytes,3,opt,name=package"`
}

type IgnoreRulePackage struct {
	Name         string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	Version      string `json:"version,omitempty" protobuf:"bytes,2,opt,name=version"`
	Language     string `json:"language" protobuf:"bytes,6,opt,name=language"`
	Type         string `json:"type,omitempty" protobuf:"bytes,3,opt,name=type"`
	Location     string `json:"location,omitempty" protobuf:"bytes,4,opt,name=location"`
	UpstreamName string `json:"upstream-name,omitempty" protobuf:"bytes,5,opt,name=upstreamname"`
}

type Distribution struct {
	Name    string   `json:"name" protobuf:"bytes,1,req,name=name"`
	Version string   `json:"version" protobuf:"bytes,2,req,name=version"`
	IDLike  []string `json:"idLike" protobuf:"bytes,3,rep,name=idLike"`
}

type Descriptor struct {
	Name                  string          `json:"name" protobuf:"bytes,1,req,name=name"`
	Version               string          `json:"version" protobuf:"bytes,2,req,name=version"`
	Configuration         json.RawMessage `json:"configuration,omitempty" protobuf:"bytes,3,opt,name=configuration"`
	VulnerabilityDBStatus json.RawMessage `json:"db,omitempty" protobuf:"bytes,4,opt,name=db"`
}

type Source struct {
	Type   string          `json:"type" protobuf:"bytes,1,req,name=type"`
	Target json.RawMessage `json:"target,omitempty" protobuf:"bytes,2,opt,name=target"`
}

// GrypeDocument is the document that represents the vulnerability manifest in
// the Grype’s JSON format
type GrypeDocument struct {
	Matches        []Match        `json:"matches" protobuf:"bytes,1,rep,name=matches"`
	IgnoredMatches []IgnoredMatch `json:"ignoredMatches,omitempty" protobuf:"bytes,2,rep,name=ignoredMatches"`
	Source         *Source        `json:"source" protobuf:"bytes,3,req,name=source"`
	Distro         Distribution   `json:"distro" protobuf:"bytes,4,req,name=distro"`
	Descriptor_    Descriptor     `json:"descriptor" protobuf:"bytes,5,req,name=descriptor"`
}
