package wardle

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Annotator struct {
	Annotator string
	// including AnnotatorType: one of "Person", "Organization" or "Tool"
	AnnotatorType string
}

// UnmarshalJSON takes an annotator in the typical one-line format and parses it into an Annotator struct.
// This function is also used when unmarshalling YAML
func (a *Annotator) UnmarshalJSON(data []byte) error {
	// annotator will simply be a string
	annotatorStr := string(data)
	annotatorStr = strings.Trim(annotatorStr, "\"")

	annotatorFields := strings.SplitN(annotatorStr, ": ", 2)

	if len(annotatorFields) != 2 {
		return fmt.Errorf("failed to parse Annotator '%s'", annotatorStr)
	}

	a.AnnotatorType = annotatorFields[0]
	a.Annotator = annotatorFields[1]

	return nil
}

// MarshalJSON converts the receiver into a slice of bytes representing an Annotator in string form.
// This function is also used when marshalling to YAML
func (a Annotator) MarshalJSON() ([]byte, error) {
	if a.Annotator != "" {
		return json.Marshal(fmt.Sprintf("%s: %s", a.AnnotatorType, a.Annotator))
	}

	return []byte{}, nil
}

// ChecksumAlgorithm represents the algorithm used to generate the file checksum in the Checksum struct.
type ChecksumAlgorithm string

// The checksum algorithms mentioned in the spdxv2.2.0 https://spdx.github.io/spdx-spec/4-file-information/#44-file-checksum
const (
	SHA224      ChecksumAlgorithm = "SHA224"
	SHA1        ChecksumAlgorithm = "SHA1"
	SHA256      ChecksumAlgorithm = "SHA256"
	SHA384      ChecksumAlgorithm = "SHA384"
	SHA512      ChecksumAlgorithm = "SHA512"
	MD2         ChecksumAlgorithm = "MD2"
	MD4         ChecksumAlgorithm = "MD4"
	MD5         ChecksumAlgorithm = "MD5"
	MD6         ChecksumAlgorithm = "MD6"
	SHA3_256    ChecksumAlgorithm = "SHA3-256"
	SHA3_384    ChecksumAlgorithm = "SHA3-384"
	SHA3_512    ChecksumAlgorithm = "SHA3-512"
	BLAKE2b_256 ChecksumAlgorithm = "BLAKE2b-256"
	BLAKE2b_384 ChecksumAlgorithm = "BLAKE2b-384"
	BLAKE2b_512 ChecksumAlgorithm = "BLAKE2b-512"
	BLAKE3      ChecksumAlgorithm = "BLAKE3"
	ADLER32     ChecksumAlgorithm = "ADLER32"
)

// Checksum provides a unique identifier to match analysis information on each specific file in a package.
// The Algorithm field describes the ChecksumAlgorithm used and the Value represents the file checksum
type Checksum struct {
	Algorithm ChecksumAlgorithm `json:"algorithm"`
	Value     string            `json:"checksumValue"`
}

// Creator is a wrapper around the Creator SPDX field. The SPDX field contains two values, which requires special
// handling in order to marshal/unmarshal it to/from Go data types.
type Creator struct {
	Creator string
	// CreatorType should be one of "Person", "Organization", or "Tool"
	CreatorType string
}

// UnmarshalJSON takes an annotator in the typical one-line format and parses it into a Creator struct.
// This function is also used when unmarshalling YAML
func (c *Creator) UnmarshalJSON(data []byte) error {
	str := string(data)
	str = strings.Trim(str, "\"")
	fields := strings.SplitN(str, ": ", 2)

	if len(fields) != 2 {
		return fmt.Errorf("failed to parse Creator '%s'", str)
	}

	c.CreatorType = fields[0]
	c.Creator = fields[1]

	return nil
}

// MarshalJSON converts the receiver into a slice of bytes representing a Creator in string form.
// This function is also used with marshalling to YAML
func (c Creator) MarshalJSON() ([]byte, error) {
	if c.Creator != "" {
		return json.Marshal(fmt.Sprintf("%s: %s", c.CreatorType, c.Creator))
	}

	return []byte{}, nil
}

// Constants for various string types
const (
	// F.2 Security types
	TypeSecurityCPE23Type string = "cpe23Type"
	TypeSecurityCPE22Type string = "cpe22Type"
	TypeSecurityAdvisory  string = "advisory"
	TypeSecurityFix       string = "fix"
	TypeSecurityUrl       string = "url"
	TypeSecuritySwid      string = "swid"

	// F.3 Package-Manager types
	TypePackageManagerMavenCentral string = "maven-central"
	TypePackageManagerNpm          string = "npm"
	TypePackageManagerNuGet        string = "nuget"
	TypePackageManagerBower        string = "bower"
	TypePackageManagerPURL         string = "purl"

	// 11.1 Relationship field types
	TypeRelationshipDescribe                  string = "DESCRIBES"
	TypeRelationshipDescribeBy                string = "DESCRIBED_BY"
	TypeRelationshipContains                  string = "CONTAINS"
	TypeRelationshipContainedBy               string = "CONTAINED_BY"
	TypeRelationshipDependsOn                 string = "DEPENDS_ON"
	TypeRelationshipDependencyOf              string = "DEPENDENCY_OF"
	TypeRelationshipBuildDependencyOf         string = "BUILD_DEPENDENCY_OF"
	TypeRelationshipDevDependencyOf           string = "DEV_DEPENDENCY_OF"
	TypeRelationshipOptionalDependencyOf      string = "OPTIONAL_DEPENDENCY_OF"
	TypeRelationshipProvidedDependencyOf      string = "PROVIDED_DEPENDENCY_OF"
	TypeRelationshipTestDependencyOf          string = "TEST_DEPENDENCY_OF"
	TypeRelationshipRuntimeDependencyOf       string = "RUNTIME_DEPENDENCY_OF"
	TypeRelationshipExampleOf                 string = "EXAMPLE_OF"
	TypeRelationshipGenerates                 string = "GENERATES"
	TypeRelationshipGeneratedFrom             string = "GENERATED_FROM"
	TypeRelationshipAncestorOf                string = "ANCESTOR_OF"
	TypeRelationshipDescendantOf              string = "DESCENDANT_OF"
	TypeRelationshipVariantOf                 string = "VARIANT_OF"
	TypeRelationshipDistributionArtifact      string = "DISTRIBUTION_ARTIFACT"
	TypeRelationshipPatchFor                  string = "PATCH_FOR"
	TypeRelationshipPatchApplied              string = "PATCH_APPLIED"
	TypeRelationshipCopyOf                    string = "COPY_OF"
	TypeRelationshipFileAdded                 string = "FILE_ADDED"
	TypeRelationshipFileDeleted               string = "FILE_DELETED"
	TypeRelationshipFileModified              string = "FILE_MODIFIED"
	TypeRelationshipExpandedFromArchive       string = "EXPANDED_FROM_ARCHIVE"
	TypeRelationshipDynamicLink               string = "DYNAMIC_LINK"
	TypeRelationshipStaticLink                string = "STATIC_LINK"
	TypeRelationshipDataFileOf                string = "DATA_FILE_OF"
	TypeRelationshipTestCaseOf                string = "TEST_CASE_OF"
	TypeRelationshipBuildToolOf               string = "BUILD_TOOL_OF"
	TypeRelationshipDevToolOf                 string = "DEV_TOOL_OF"
	TypeRelationshipTestOf                    string = "TEST_OF"
	TypeRelationshipTestToolOf                string = "TEST_TOOL_OF"
	TypeRelationshipDocumentationOf           string = "DOCUMENTATION_OF"
	TypeRelationshipOptionalComponentOf       string = "OPTIONAL_COMPONENT_OF"
	TypeRelationshipMetafileOf                string = "METAFILE_OF"
	TypeRelationshipPackageOf                 string = "PACKAGE_OF"
	TypeRelationshipAmends                    string = "AMENDS"
	TypeRelationshipPrerequisiteFor           string = "PREREQUISITE_FOR"
	TypeRelationshipHasPrerequisite           string = "HAS_PREREQUISITE"
	TypeRelationshipRequirementDescriptionFor string = "REQUIREMENT_DESCRIPTION_FOR"
	TypeRelationshipSpecificationFor          string = "SPECIFICATION_FOR"
	TypeRelationshipOther                     string = "OTHER"
)

const (
	spdxRefPrefix     = "SPDXRef-"
	documentRefPrefix = "DocumentRef-"
)

// ElementID represents the identifier string portion of an SPDX element
// identifier. DocElementID should be used for any attributes which can
// contain identifiers defined in a different SPDX document.
// ElementIDs should NOT contain the mandatory 'SPDXRef-' portion.
type ElementID string

// MarshalJSON returns an SPDXRef- prefixed JSON string
func (d ElementID) MarshalJSON() ([]byte, error) {
	return json.Marshal(prefixElementId(d))
}

// UnmarshalJSON validates SPDXRef- prefixes and removes them when processing ElementIDs
func (d *ElementID) UnmarshalJSON(data []byte) error {
	// SPDX identifier will simply be a string
	idStr := string(data)
	idStr = strings.Trim(idStr, "\"")

	e, err := trimElementIdPrefix(idStr)
	if err != nil {
		return err
	}
	*d = e
	return nil
}

// prefixElementId adds the SPDXRef- prefix to an element ID if it does not have one
func prefixElementId(id ElementID) string {
	val := string(id)
	if !strings.HasPrefix(val, spdxRefPrefix) {
		return spdxRefPrefix + val
	}
	return val
}

// trimElementIdPrefix removes the SPDXRef- prefix from an element ID string or returns an error if it
// does not start with SPDXRef-
func trimElementIdPrefix(id string) (ElementID, error) {
	// handle SPDXRef-
	idFields := strings.SplitN(id, spdxRefPrefix, 2)
	if len(idFields) != 2 {
		return "", fmt.Errorf("failed to parse SPDX identifier '%s'", id)
	}

	e := ElementID(idFields[1])
	return e, nil
}

// DocElementID represents an SPDX element identifier that could be defined
// in a different SPDX document, and therefore could have a "DocumentRef-"
// portion, such as Relationships and Annotations.
// ElementID is used for attributes in which a "DocumentRef-" portion cannot
// appear, such as a Package or File definition (since it is necessarily
// being defined in the present document).
// DocumentRefID will be the empty string for elements defined in the
// present document.
// DocElementIDs should NOT contain the mandatory 'DocumentRef-' or
// 'SPDXRef-' portions.
// SpecialID is used ONLY if the DocElementID matches a defined set of
// permitted special values for a particular field, e.g. "NONE" or
// "NOASSERTION" for the right-hand side of Relationships. If SpecialID
// is set, DocumentRefID and ElementRefID should be empty (and vice versa).
type DocElementID struct {
	DocumentRefID string
	ElementRefID  ElementID
	SpecialID     string
}

// MarshalJSON converts the receiver into a slice of bytes representing a DocElementID in string form.
// This function is also used when marshalling to YAML
func (d DocElementID) MarshalJSON() ([]byte, error) {
	if d.DocumentRefID != "" && d.ElementRefID != "" {
		idStr := prefixElementId(d.ElementRefID)
		return json.Marshal(fmt.Sprintf("%s%s:%s", documentRefPrefix, d.DocumentRefID, idStr))
	} else if d.ElementRefID != "" {
		return json.Marshal(prefixElementId(d.ElementRefID))
	} else if d.SpecialID != "" {
		return json.Marshal(d.SpecialID)
	}

	return []byte{}, fmt.Errorf("failed to marshal empty DocElementID")
}

// UnmarshalJSON takes a SPDX Identifier string parses it into a DocElementID struct.
// This function is also used when unmarshalling YAML
func (d *DocElementID) UnmarshalJSON(data []byte) (err error) {
	// SPDX identifier will simply be a string
	idStr := string(data)
	idStr = strings.Trim(idStr, "\"")

	// handle special cases
	if idStr == "NONE" || idStr == "NOASSERTION" {
		d.SpecialID = idStr
		return nil
	}

	var idFields []string
	// handle DocumentRef- if present
	if strings.HasPrefix(idStr, documentRefPrefix) {
		// strip out the "DocumentRef-" so we can get the value
		idFields = strings.SplitN(idStr, documentRefPrefix, 2)
		idStr = idFields[1]

		// an SPDXRef can appear after a DocumentRef, separated by a colon
		idFields = strings.SplitN(idStr, ":", 2)
		d.DocumentRefID = idFields[0]

		if len(idFields) == 2 {
			idStr = idFields[1]
		} else {
			return nil
		}
	}

	d.ElementRefID, err = trimElementIdPrefix(idStr)
	return err
}

// TODO: add equivalents for LicenseRef- identifiers

// MakeDocElementID takes strings (without prefixes) for the DocumentRef-
// and SPDXRef- identifiers, and returns a DocElementID. An empty string
// should be used for the DocumentRef- portion if it is referring to the
// present document.
func MakeDocElementID(docRef string, eltRef string) DocElementID {
	return DocElementID{
		DocumentRefID: docRef,
		ElementRefID:  ElementID(eltRef),
	}
}

// MakeDocElementSpecial takes a "special" string (e.g. "NONE" or
// "NOASSERTION" for the right side of a Relationship), nd returns
// a DocElementID with it in the SpecialID field. Other fields will
// be empty.
func MakeDocElementSpecial(specialID string) DocElementID {
	return DocElementID{SpecialID: specialID}
}

// RenderElementID takes an ElementID and returns the string equivalent,
// with the SPDXRef- prefix reinserted.
func RenderElementID(eID ElementID) string {
	return spdxRefPrefix + string(eID)
}

// RenderDocElementID takes a DocElementID and returns the string equivalent,
// with the SPDXRef- prefix (and, if applicable, the DocumentRef- prefix)
// reinserted. If a SpecialID is present, it will be rendered verbatim and
// DocumentRefID and ElementRefID will be ignored.
func RenderDocElementID(deID DocElementID) string {
	if deID.SpecialID != "" {
		return deID.SpecialID
	}
	prefix := ""
	if deID.DocumentRefID != "" {
		prefix = documentRefPrefix + deID.DocumentRefID + ":"
	}
	return prefix + spdxRefPrefix + string(deID.ElementRefID)
}

type Supplier struct {
	// can be "NOASSERTION"
	Supplier string
	// SupplierType can be one of "Person", "Organization", or empty if Supplier is "NOASSERTION"
	SupplierType string
}

// UnmarshalJSON takes a supplier in the typical one-line format and parses it into a Supplier struct.
// This function is also used when unmarshalling YAML
func (s *Supplier) UnmarshalJSON(data []byte) error {
	// the value is just a string presented as a slice of bytes
	supplierStr := string(data)
	supplierStr = strings.Trim(supplierStr, "\"")

	if supplierStr == "NOASSERTION" {
		s.Supplier = supplierStr
		return nil
	}

	supplierFields := strings.SplitN(supplierStr, ": ", 2)

	if len(supplierFields) != 2 {
		return fmt.Errorf("failed to parse Supplier '%s'", supplierStr)
	}

	s.SupplierType = supplierFields[0]
	s.Supplier = supplierFields[1]

	return nil
}

// MarshalJSON converts the receiver into a slice of bytes representing a Supplier in string form.
// This function is also used when marshalling to YAML
func (s Supplier) MarshalJSON() ([]byte, error) {
	if s.Supplier == "NOASSERTION" {
		return json.Marshal(s.Supplier)
	} else if s.SupplierType != "" && s.Supplier != "" {
		return json.Marshal(fmt.Sprintf("%s: %s", s.SupplierType, s.Supplier))
	}

	return []byte{}, fmt.Errorf("failed to marshal invalid Supplier: %+v", s)
}

type Originator struct {
	// can be "NOASSERTION"
	Originator string
	// OriginatorType can be one of "Person", "Organization", or empty if Originator is "NOASSERTION"
	OriginatorType string
}

// UnmarshalJSON takes an originator in the typical one-line format and parses it into an Originator struct.
// This function is also used when unmarshalling YAML
func (o *Originator) UnmarshalJSON(data []byte) error {
	// the value is just a string presented as a slice of bytes
	originatorStr := string(data)
	originatorStr = strings.Trim(originatorStr, "\"")

	if originatorStr == "NOASSERTION" {
		o.Originator = originatorStr
		return nil
	}

	originatorFields := strings.SplitN(originatorStr, ": ", 2)

	if len(originatorFields) != 2 {
		return fmt.Errorf("failed to parse Originator '%s'", originatorStr)
	}

	o.OriginatorType = originatorFields[0]
	o.Originator = originatorFields[1]

	return nil
}

// MarshalJSON converts the receiver into a slice of bytes representing an Originator in string form.
// This function is also used when marshalling to YAML
func (o Originator) MarshalJSON() ([]byte, error) {
	if o.Originator == "NOASSERTION" {
		return json.Marshal(o.Originator)
	} else if o.Originator != "" {
		return json.Marshal(fmt.Sprintf("%s: %s", o.OriginatorType, o.Originator))
	}

	return []byte{}, nil
}

type PackageVerificationCode struct {
	// Cardinality: mandatory, one if filesAnalyzed is true / omitted;
	//              zero (must be omitted) if filesAnalyzed is false
	Value string `json:"packageVerificationCodeValue"`
	// Spec also allows specifying files to exclude from the
	// verification code algorithm; intended to enable exclusion of
	// the SPDX document file itself.
	ExcludedFiles []string `json:"packageVerificationCodeExcludedFiles,omitempty"`
}

type SnippetRangePointer struct {
	// 5.3: Snippet Byte Range: [start byte]:[end byte]
	// Cardinality: mandatory, one
	Offset int `json:"offset,omitempty"`

	// 5.4: Snippet Line Range: [start line]:[end line]
	// Cardinality: optional, one
	LineNumber int `json:"lineNumber,omitempty"`

	FileSPDXIdentifier ElementID `json:"reference"`
}

type SnippetRange struct {
	StartPointer SnippetRangePointer `json:"startPointer"`
	EndPointer   SnippetRangePointer `json:"endPointer"`
}

// Annotation is an Annotation section of an SPDX Document for version 2.3 of the spec.
type Annotation struct {
	// 12.1: Annotator
	// Cardinality: conditional (mandatory, one) if there is an Annotation
	Annotator Annotator `json:"annotator"`

	// 12.2: Annotation Date: YYYY-MM-DDThh:mm:ssZ
	// Cardinality: conditional (mandatory, one) if there is an Annotation
	AnnotationDate string `json:"annotationDate"`

	// 12.3: Annotation Type: "REVIEW" or "OTHER"
	// Cardinality: conditional (mandatory, one) if there is an Annotation
	AnnotationType string `json:"annotationType"`

	// 12.4: SPDX Identifier Reference
	// Cardinality: conditional (mandatory, one) if there is an Annotation
	// This field is not used in hierarchical data formats where the referenced element is clear, such as JSON or YAML.
	AnnotationSPDXIdentifier DocElementID `json:"-" yaml:"-"`

	// 12.5: Annotation Comment
	// Cardinality: conditional (mandatory, one) if there is an Annotation
	AnnotationComment string `json:"comment"`
}

// CreationInfo is a Document Creation Information section of an
// SPDX Document for version 2.3 of the spec.
type CreationInfo struct {
	// 6.7: License List Version
	// Cardinality: optional, one
	LicenseListVersion string `json:"licenseListVersion"`

	// 6.8: Creators: may have multiple keys for Person, Organization
	//      and/or Tool
	// Cardinality: mandatory, one or many
	Creators []Creator `json:"creators"`

	// 6.9: Created: data format YYYY-MM-DDThh:mm:ssZ
	// Cardinality: mandatory, one
	Created string `json:"created"`

	// 6.10: Creator Comment
	// Cardinality: optional, one
	CreatorComment string `json:"comment,omitempty"`
}

// ExternalDocumentRef is a reference to an external SPDX document
// as defined in section 6.6 for version 2.3 of the spec.
type ExternalDocumentRef struct {
	// DocumentRefID is the ID string defined in the start of the
	// reference. It should _not_ contain the "DocumentRef-" part
	// of the mandatory ID string.
	DocumentRefID string `json:"externalDocumentId"`

	// URI is the URI defined for the external document
	URI string `json:"spdxDocument"`

	// Checksum is the actual hash data
	Checksum Checksum `json:"checksum"`
}

// Document is an SPDX Document for version 2.3 of the spec.
// See https://spdx.github.io/spdx-spec/v2.3/document-creation-information
type Document struct {
	// Added
	DocumentDescribes []string `json:"documentDescribes"`

	// 6.1: SPDX Version; should be in the format "SPDX-2.3"
	// Cardinality: mandatory, one
	SPDXVersion string `json:"spdxVersion"`

	// 6.2: Data License; should be "CC0-1.0"
	// Cardinality: mandatory, one
	DataLicense string `json:"dataLicense"`

	// 6.3: SPDX Identifier; should be "DOCUMENT" to represent
	//      mandatory identifier of SPDXRef-DOCUMENT
	// Cardinality: mandatory, one
	SPDXIdentifier ElementID `json:"SPDXID"`

	// 6.4: Document Name
	// Cardinality: mandatory, one
	DocumentName string `json:"name"`

	// 6.5: Document Namespace
	// Cardinality: mandatory, one
	DocumentNamespace string `json:"documentNamespace"`

	// 6.6: External Document References
	// Cardinality: optional, one or many
	ExternalDocumentReferences []ExternalDocumentRef `json:"externalDocumentRefs,omitempty"`

	// 6.11: Document Comment
	// Cardinality: optional, one
	DocumentComment string `json:"comment,omitempty"`

	CreationInfo  *CreationInfo   `json:"creationInfo"`
	Packages      []*Package      `json:"packages,omitempty"`
	Files         []*File         `json:"files,omitempty"`
	OtherLicenses []*OtherLicense `json:"hasExtractedLicensingInfos,omitempty"`
	Relationships []*Relationship `json:"relationships,omitempty"`
	Annotations   []*Annotation   `json:"annotations,omitempty"`
	Snippets      []Snippet       `json:"snippets,omitempty"`

	// DEPRECATED in version 2.0 of spec
	Reviews []*Review `json:"-" yaml:"-"`
}

// File is a File section of an SPDX Document for version 2.3 of the spec.
type File struct {
	// 8.1: File Name
	// Cardinality: mandatory, one
	FileName string `json:"fileName"`

	// 8.2: File SPDX Identifier: "SPDXRef-[idstring]"
	// Cardinality: mandatory, one
	FileSPDXIdentifier ElementID `json:"SPDXID"`

	// 8.3: File Types
	// Cardinality: optional, multiple
	FileTypes []string `json:"fileTypes,omitempty"`

	// 8.4: File Checksum: may have keys for SHA1, SHA256, MD5, SHA3-256, SHA3-384, SHA3-512, BLAKE2b-256, BLAKE2b-384, BLAKE2b-512, BLAKE3, ADLER32
	// Cardinality: mandatory, one SHA1, others may be optionally provided
	Checksums []Checksum `json:"checksums"`

	// 8.5: Concluded License: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one
	LicenseConcluded string `json:"licenseConcluded,omitempty"`

	// 8.6: License Information in File: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one or many
	LicenseInfoInFiles []string `json:"licenseInfoInFiles,omitempty"`

	// 8.7: Comments on License
	// Cardinality: optional, one
	LicenseComments string `json:"licenseComments,omitempty"`

	// 8.8: Copyright Text: copyright notice(s) text, "NONE" or "NOASSERTION"
	// Cardinality: mandatory, one
	FileCopyrightText string `json:"copyrightText"`

	// DEPRECATED in version 2.1 of spec
	// 8.9-8.11: Artifact of Project variables (defined below)
	// Cardinality: optional, one or many
	ArtifactOfProjects []*ArtifactOfProject `json:"artifactOfs,omitempty"`

	// 8.12: File Comment
	// Cardinality: optional, one
	FileComment string `json:"comment,omitempty"`

	// 8.13: File Notice
	// Cardinality: optional, one
	FileNotice string `json:"noticeText,omitempty"`

	// 8.14: File Contributor
	// Cardinality: optional, one or many
	FileContributors []string `json:"fileContributors,omitempty"`

	// 8.15: File Attribution Text
	// Cardinality: optional, one or many
	FileAttributionTexts []string `json:"attributionTexts,omitempty"`

	// DEPRECATED in version 2.0 of spec
	// 8.16: File Dependencies
	// Cardinality: optional, one or many
	FileDependencies []string `json:"fileDependencies,omitempty"`

	// Snippets contained in this File
	// Note that Snippets could be defined in a different Document! However,
	// the only ones that _THIS_ document can contain are this ones that are
	// defined here -- so this should just be an ElementID.
	Snippets map[ElementID]*Snippet `json:"-" yaml:"-"`

	Annotations []Annotation `json:"annotations,omitempty"`
}

// ArtifactOfProject is a DEPRECATED collection of data regarding
// a Package, as defined in sections 8.9-8.11 in version 2.3 of the spec.
// NOTE: the JSON schema does not define the structure of this object:
// https://github.com/spdx/spdx-spec/blob/development/v2.3.1/schemas/spdx-schema.json#L480
type ArtifactOfProject struct {

	// DEPRECATED in version 2.1 of spec
	// 8.9: Artifact of Project Name
	// Cardinality: conditional, required if present, one per AOP
	Name string `json:"name"`

	// DEPRECATED in version 2.1 of spec
	// 8.10: Artifact of Project Homepage: URL or "UNKNOWN"
	// Cardinality: optional, one per AOP
	HomePage string `json:"homePage"`

	// DEPRECATED in version 2.1 of spec
	// 8.11: Artifact of Project Uniform Resource Identifier
	// Cardinality: optional, one per AOP
	URI string `json:"URI"`
}

// OtherLicense is an Other License Information section of an
// SPDX Document for version 2.3 of the spec.
type OtherLicense struct {
	// 10.1: License Identifier: "LicenseRef-[idstring]"
	// Cardinality: conditional (mandatory, one) if license is not
	//              on SPDX License List
	LicenseIdentifier string `json:"licenseId"`

	// 10.2: Extracted Text
	// Cardinality: conditional (mandatory, one) if there is a
	//              License Identifier assigned
	ExtractedText string `json:"extractedText"`

	// 10.3: License Name: single line of text or "NOASSERTION"
	// Cardinality: conditional (mandatory, one) if license is not
	//              on SPDX License List
	LicenseName string `json:"name,omitempty"`

	// 10.4: License Cross Reference
	// Cardinality: conditional (optional, one or many) if license
	//              is not on SPDX License List
	LicenseCrossReferences []string `json:"seeAlsos,omitempty"`

	// 10.5: License Comment
	// Cardinality: optional, one
	LicenseComment string `json:"comment,omitempty"`
}

// Package is a Package section of an SPDX Document for version 2.3 of the spec.
type Package struct {
	// Added
	HasFiles []string `json:"hasFiles,omitempty"`

	// NOT PART OF SPEC
	// flag: does this "package" contain files that were in fact "unpackaged",
	// e.g. included directly in the Document without being in a Package?
	IsUnpackaged bool `json:"-" yaml:"-"`

	// 7.1: Package Name
	// Cardinality: mandatory, one
	PackageName string `json:"name"`

	// 7.2: Package SPDX Identifier: "SPDXRef-[idstring]"
	// Cardinality: mandatory, one
	PackageSPDXIdentifier ElementID `json:"SPDXID"`

	// 7.3: Package Version
	// Cardinality: optional, one
	PackageVersion string `json:"versionInfo,omitempty"`

	// 7.4: Package File Name
	// Cardinality: optional, one
	PackageFileName string `json:"packageFileName,omitempty"`

	// 7.5: Package Supplier: may have single result for either Person or Organization,
	//                        or NOASSERTION
	// Cardinality: optional, one
	PackageSupplier *Supplier `json:"supplier,omitempty"`

	// 7.6: Package Originator: may have single result for either Person or Organization,
	//                          or NOASSERTION
	// Cardinality: optional, one
	PackageOriginator *Originator `json:"originator,omitempty"`

	// 7.7: Package Download Location
	// Cardinality: mandatory, one
	PackageDownloadLocation string `json:"downloadLocation"`

	// 7.8: FilesAnalyzed
	// Cardinality: optional, one; default value is "true" if omitted
	FilesAnalyzed bool `json:"filesAnalyzed,omitempty"`
	// NOT PART OF SPEC: did FilesAnalyzed tag appear?
	IsFilesAnalyzedTagPresent bool `json:"-" yaml:"-"`

	// 7.9: Package Verification Code
	// Cardinality: if FilesAnalyzed == true must be present, if FilesAnalyzed == false must be omitted
	PackageVerificationCode *PackageVerificationCode `json:"packageVerificationCode,omitempty"`

	// 7.10: Package Checksum: may have keys for SHA1, SHA256, SHA512, MD5, SHA3-256, SHA3-384, SHA3-512, BLAKE2b-256, BLAKE2b-384, BLAKE2b-512, BLAKE3, ADLER32
	// Cardinality: optional, one or many
	PackageChecksums []Checksum `json:"checksums,omitempty"`

	// 7.11: Package Home Page
	// Cardinality: optional, one
	PackageHomePage string `json:"homepage,omitempty"`

	// 7.12: Source Information
	// Cardinality: optional, one
	PackageSourceInfo string `json:"sourceInfo,omitempty"`

	// 7.13: Concluded License: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one
	PackageLicenseConcluded string `json:"licenseConcluded,omitempty"`

	// 7.14: All Licenses Info from Files: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one or many if filesAnalyzed is true / omitted;
	//              zero (must be omitted) if filesAnalyzed is false
	PackageLicenseInfoFromFiles []string `json:"licenseInfoFromFiles,omitempty"`

	// 7.15: Declared License: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one
	PackageLicenseDeclared string `json:"licenseDeclared,omitempty"`

	// 7.16: Comments on License
	// Cardinality: optional, one
	PackageLicenseComments string `json:"licenseComments,omitempty"`

	// 7.17: Copyright Text: copyright notice(s) text, "NONE" or "NOASSERTION"
	// Cardinality: mandatory, one
	PackageCopyrightText string `json:"copyrightText"`

	// 7.18: Package Summary Description
	// Cardinality: optional, one
	PackageSummary string `json:"summary,omitempty"`

	// 7.19: Package Detailed Description
	// Cardinality: optional, one
	PackageDescription string `json:"description,omitempty"`

	// 7.20: Package Comment
	// Cardinality: optional, one
	PackageComment string `json:"comment,omitempty"`

	// 7.21: Package External Reference
	// Cardinality: optional, one or many
	PackageExternalReferences []*PackageExternalReference `json:"externalRefs,omitempty"`

	// 7.22: Package External Reference Comment
	// Cardinality: conditional (optional, one) for each External Reference
	// contained within PackageExternalReference2_1 struct, if present

	// 7.23: Package Attribution Text
	// Cardinality: optional, one or many
	PackageAttributionTexts []string `json:"attributionTexts,omitempty"`

	// 7.24: Primary Package Purpose
	// Cardinality: optional, one or many
	// Allowed values: APPLICATION, FRAMEWORK, LIBRARY, CONTAINER, OPERATING-SYSTEM, DEVICE, FIRMWARE, SOURCE, ARCHIVE, FILE, INSTALL, OTHER
	PrimaryPackagePurpose string `json:"primaryPackagePurpose,omitempty"`

	// 7.25: Release Date: YYYY-MM-DDThh:mm:ssZ
	// Cardinality: optional, one
	ReleaseDate string `json:"releaseDate,omitempty"`

	// 7.26: Build Date: YYYY-MM-DDThh:mm:ssZ
	// Cardinality: optional, one
	BuiltDate string `json:"builtDate,omitempty"`

	// 7.27: Valid Until Date: YYYY-MM-DDThh:mm:ssZ
	// Cardinality: optional, one
	ValidUntilDate string `json:"validUntilDate,omitempty"`

	// Files contained in this Package
	Files []*File `json:"files,omitempty"`

	Annotations []Annotation `json:"annotations,omitempty"`
}

// PackageExternalReference is an External Reference to additional info
// about a Package, as defined in section 7.21 in version 2.3 of the spec.
type PackageExternalReference struct {
	// category is "SECURITY", "PACKAGE-MANAGER" or "OTHER"
	Category string `json:"referenceCategory"`

	// type is an [idstring] as defined in Appendix VI;
	// called RefType here due to "type" being a Golang keyword
	RefType string `json:"referenceType"`

	// locator is a unique string to access the package-specific
	// info, metadata or content within the target location
	Locator string `json:"referenceLocator"`

	// 7.22: Package External Reference Comment
	// Cardinality: conditional (optional, one) for each External Reference
	ExternalRefComment string `json:"comment,omitempty"`
}

// Relationship is a Relationship section of an SPDX Document for
// version 2.3 of the spec.
type Relationship struct {

	// 11.1: Relationship
	// Cardinality: optional, one or more; one per Relationship
	//              one mandatory for SPDX Document with multiple packages
	// RefA and RefB are first and second item
	// Relationship is type from 11.1.1
	RefA         DocElementID `json:"spdxElementId"`
	RefB         DocElementID `json:"relatedSpdxElement"`
	Relationship string              `json:"relationshipType"`

	// 11.2: Relationship Comment
	// Cardinality: optional, one
	RelationshipComment string `json:"comment,omitempty"`
}

// Review is a Review section of an SPDX Document for version 2.3 of the spec.
// DEPRECATED in version 2.0 of spec; retained here for compatibility.
type Review struct {

	// DEPRECATED in version 2.0 of spec
	// 13.1: Reviewer
	// Cardinality: optional, one
	Reviewer string
	// including AnnotatorType: one of "Person", "Organization" or "Tool"
	ReviewerType string

	// DEPRECATED in version 2.0 of spec
	// 13.2: Review Date: YYYY-MM-DDThh:mm:ssZ
	// Cardinality: conditional (mandatory, one) if there is a Reviewer
	ReviewDate string

	// DEPRECATED in version 2.0 of spec
	// 13.3: Review Comment
	// Cardinality: optional, one
	ReviewComment string
}

// Snippet is a Snippet section of an SPDX Document for version 2.3 of the spec.
type Snippet struct {

	// 9.1: Snippet SPDX Identifier: "SPDXRef-[idstring]"
	// Cardinality: mandatory, one
	SnippetSPDXIdentifier ElementID `json:"SPDXID"`

	// 9.2: Snippet from File SPDX Identifier
	// Cardinality: mandatory, one
	SnippetFromFileSPDXIdentifier ElementID `json:"snippetFromFile"`

	// Ranges denotes the start/end byte offsets or line numbers that the snippet is relevant to
	Ranges []SnippetRange `json:"ranges"`

	// 9.5: Snippet Concluded License: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one
	SnippetLicenseConcluded string `json:"licenseConcluded,omitempty"`

	// 9.6: License Information in Snippet: SPDX License Expression, "NONE" or "NOASSERTION"
	// Cardinality: optional, one or many
	LicenseInfoInSnippet []string `json:"licenseInfoInSnippets,omitempty"`

	// 9.7: Snippet Comments on License
	// Cardinality: optional, one
	SnippetLicenseComments string `json:"licenseComments,omitempty"`

	// 9.8: Snippet Copyright Text: copyright notice(s) text, "NONE" or "NOASSERTION"
	// Cardinality: mandatory, one
	SnippetCopyrightText string `json:"copyrightText"`

	// 9.9: Snippet Comment
	// Cardinality: optional, one
	SnippetComment string `json:"comment,omitempty"`

	// 9.10: Snippet Name
	// Cardinality: optional, one
	SnippetName string `json:"name,omitempty"`

	// 9.11: Snippet Attribution Text
	// Cardinality: optional, one or many
	SnippetAttributionTexts []string `json:"-" yaml:"-"`
}
