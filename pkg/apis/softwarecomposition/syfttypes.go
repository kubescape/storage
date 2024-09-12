package softwarecomposition

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/anchore/syft/syft/license"
	"github.com/anchore/syft/syft/source"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/packagemetadata"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/sourcemetadata"
)

type Digest struct {
	Algorithm string
	Value     string
}

type LocationMetadata struct {
	Annotations map[string]string // Arbitrary key-value pairs that can be used to annotate a location
}

// Coordinates contains the minimal information needed to describe how to find a file within any possible source object (e.g. image and directory sources)
type Coordinates struct {
	RealPath     string // The path where all path ancestors have no hardlinks / symlinks
	FileSystemID string // An ID representing the filesystem. For container images, this is a layer digest. For directories or a root filesystem, this is blank.
}

// Location represents a path relative to a particular filesystem resolved to a specific file.Reference. This struct is used as a key
// in content fetching to uniquely identify a file relative to a request (the VirtualPath).
type Location struct {
	LocationData     `cyclonedx:""`
	LocationMetadata `cyclonedx:""`
}

type LocationData struct {
	Coordinates `cyclonedx:""` // Empty string here means there is no intermediate property name, e.g. syft:locations:0:path without "coordinates"
	// note: it is IMPORTANT to ignore anything but the coordinates for a Location when considering the ID (hash value)
	// since the coordinates are the minimally correct ID for a location (symlinks should not come into play)
	VirtualPath string `hash:"ignore" json:"accessPath"` // The path to the file which may or may not have hardlinks / symlinks
}

// SyftSource object represents the thing that was cataloged
type SyftSource struct {
	ID       string
	Name     string
	Version  string
	Type     string
	Metadata json.RawMessage
}

// sourceUnpacker is used to unmarshal SyftSource objects
type sourceUnpacker struct {
	ID       string
	Name     string
	Version  string
	Type     string
	Metadata json.RawMessage
	Target   json.RawMessage // pre-v9 schema support
}

// UnmarshalJSON populates a source object from JSON bytes.
func (s *SyftSource) UnmarshalJSON(b []byte) error {
	var unpacker sourceUnpacker
	err := json.Unmarshal(b, &unpacker)
	if err != nil {
		return err
	}

	s.Name = unpacker.Name
	s.Version = unpacker.Version
	s.Type = unpacker.Type
	s.ID = unpacker.ID

	if len(unpacker.Target) > 0 {
		s.Type = cleanPreSchemaV9MetadataType(s.Type)
		metadata, err := extractPreSchemaV9Metadata(s.Type, unpacker.Target)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		s.Metadata = encoded
		if err != nil {
			return fmt.Errorf("unable to extract pre-schema-v9 source metadata: %w", err)
		}
		return nil
	}

	return unpackSrcMetadata(s, unpacker)
}

func unpackSrcMetadata(s *SyftSource, unpacker sourceUnpacker) error {
	if s.Type == "" {
		// there are some cases where the type is not set. e.g. the object returned from the watcher
		return nil
	}

	rt := sourcemetadata.ReflectTypeFromJSONName(s.Type)
	if rt == nil {
		return fmt.Errorf("unable to find source metadata type=%q", s.Type)
	}

	val := reflect.New(rt).Interface()
	if len(unpacker.Metadata) > 0 {
		if err := json.Unmarshal(unpacker.Metadata, val); err != nil {
			return err
		}
	}

	metadata := reflect.ValueOf(val).Elem().Interface()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	s.Metadata = encoded

	return nil
}

func cleanPreSchemaV9MetadataType(t string) string {
	t = strings.ToLower(t)
	if t == "dir" {
		return "directory"
	}
	return t
}

func extractPreSchemaV9Metadata(t string, target []byte) (interface{}, error) {
	switch t {
	case "directory", "dir":
		cleanTarget, err := strconv.Unquote(string(target))
		if err != nil {
			cleanTarget = string(target)
		}

		return source.DirectoryMetadata{
			Path: cleanTarget,
		}, nil

	case "file":
		cleanTarget, err := strconv.Unquote(string(target))
		if err != nil {
			cleanTarget = string(target)
		}

		return source.FileMetadata{
			Path: cleanTarget,
		}, nil

	case "image":
		var payload source.ImageMetadata
		if err := json.Unmarshal(target, &payload); err != nil {
			return nil, err
		}
		return payload, nil

	default:
		return nil, fmt.Errorf("unsupported package metadata type: %+v", t)
	}
}

var errUnknownMetadataType = errors.New("unknown metadata type")

type SyftRelationship struct {
	Parent   string
	Child    string
	Type     string
	Metadata json.RawMessage
}

// SyftPackage represents a pkg.SyftPackage object specialized for JSON marshaling and unmarshalling.
type SyftPackage struct {
	PackageBasicData
	PackageCustomData
}

// PackageBasicData contains non-ambiguous values (type-wise) from pkg.SyftPackage.
type PackageBasicData struct {
	ID        string
	Name      string
	Version   string
	Type      string
	FoundBy   string
	Locations []Location
	Licenses  Licenses
	Language  string
	CPEs      CPEs
	PURL      string
}

// PackageBasicDataV01011 is the previous version of PackageBasicData used in schema v0.101.1.
type PackageBasicDataV01011 struct {
	ID        string
	Name      string
	Version   string
	Type      string
	FoundBy   string
	Locations []Location
	Licenses  Licenses
	Language  string
	CPEs      []string
	PURL      string
}

func PackageBasicDataFromV01011(in PackageBasicDataV01011) PackageBasicData {
	out := PackageBasicData{
		ID:        in.ID,
		Name:      in.Name,
		Version:   in.Version,
		Type:      in.Type,
		FoundBy:   in.FoundBy,
		Locations: in.Locations,
		Licenses:  in.Licenses,
		Language:  in.Language,
		CPEs:      CPEs{},
		PURL:      in.PURL,
	}
	for _, cpe := range in.CPEs {
		out.CPEs = append(out.CPEs, CPE{
			Value:  cpe,
			Source: "syft-generated",
		})
	}
	return out
}

type CPEs []CPE

type CPE struct {
	Value  string
	Source string
}

type LicenseType string

type Licenses []License

type License struct {
	Value          string
	SPDXExpression string
	Type           LicenseType
	URLs           []string
	Locations      []Location
}

func newModelLicensesFromValues(licenses []string) (ml []License) {
	for _, v := range licenses {
		expression, err := license.ParseExpression(v)
		if err != nil {
			logger.L().Warning(
				"could not find valid spdx expression for %s: %w",
				helpers.String("value", v),
				helpers.Error(err),
			)
		}
		ml = append(ml, License{
			Value:          v,
			SPDXExpression: expression,
			Type:           LicenseType(license.Declared),
		})
	}
	return ml
}

func (f *Licenses) UnmarshalJSON(b []byte) error {

	var lics []License
	if err := json.Unmarshal(b, &lics); err != nil {
		var simpleLicense []string
		if err := json.Unmarshal(b, &simpleLicense); err != nil {
			return fmt.Errorf("unable to unmarshal license: %w", err)
		}
		lics = newModelLicensesFromValues(simpleLicense)
	}
	*f = lics
	return nil
}

// PackageCustomData contains ambiguous values (type-wise) from pkg.SyftPackage.
type PackageCustomData struct {
	MetadataType string
	Metadata     json.RawMessage
}

// packageMetadataUnpacker is all values needed from SyftPackage to disambiguate ambiguous fields during json unmarshaling.
type packageMetadataUnpacker struct {
	MetadataType string
	Metadata     json.RawMessage
}

func (p *packageMetadataUnpacker) String() string {
	return fmt.Sprintf("metadataType: %s, metadata: %s", p.MetadataType, string(p.Metadata))
}

// UnmarshalJSON is a custom unmarshaller for handling basic values and values with ambiguous types.
func (p *SyftPackage) UnmarshalJSON(b []byte) error {
	var basic PackageBasicData
	if err := json.Unmarshal(b, &basic); err != nil {
		var basicV01011 PackageBasicDataV01011
		if err := json.Unmarshal(b, &basicV01011); err != nil {
			return err
		}
		basic = PackageBasicDataFromV01011(basicV01011)
	}
	p.PackageBasicData = basic

	var unpacker packageMetadataUnpacker
	if err := json.Unmarshal(b, &unpacker); err != nil {
		logger.L().Warning("failed to unmarshall into packageMetadataUnpacker: %v", helpers.Error(err))
		return err
	}

	err := unpackPkgMetadata(p, unpacker)
	if errors.Is(err, errUnknownMetadataType) {
		logger.L().Warning(
			"unknown package metadata type=%q for packageID=%q",
			helpers.Interface("type", p.MetadataType),
			helpers.Interface("packageID", p.ID),
		)
		return nil
	}

	return err
}

func unpackPkgMetadata(p *SyftPackage, unpacker packageMetadataUnpacker) error {
	if unpacker.MetadataType == "" {
		return nil
	}

	// check for legacy correction cases from schema v11 -> v12
	ty := unpacker.MetadataType
	switch unpacker.MetadataType {
	case "HackageMetadataType":
		for _, l := range p.Locations {
			if strings.HasSuffix(l.RealPath, ".yaml.lock") {
				ty = "haskell-hackage-stack-lock-entry"
				break
			} else if strings.HasSuffix(l.RealPath, ".yaml") {
				ty = "haskell-hackage-stack-entry"
				break
			}
		}
	case "RpmMetadata":
		for _, l := range p.Locations {
			if strings.HasSuffix(l.RealPath, ".rpm") {
				ty = "rpm-archive"
				break
			}
		}
	case "RustCargoPackageMetadata":
		var found bool
		for _, l := range p.Locations {
			if strings.HasSuffix(strings.ToLower(l.RealPath), "cargo.lock") {
				ty = "rust-cargo-lock-entry"
				found = true
				break
			}
		}
		if !found {
			ty = "rust-cargo-audit-entry"
		}
	}
	p.MetadataType = ty

	typ := packagemetadata.ReflectTypeFromJSONName(ty)
	if typ == nil {
		// capture unknown metadata as a generic struct
		if len(unpacker.Metadata) > 0 {
			var val interface{}
			if err := json.Unmarshal(unpacker.Metadata, &val); err != nil {
				return err
			}
			encoded, err := json.Marshal(val)
			if err != nil {
				return err
			}
			p.Metadata = encoded
		}

		return errUnknownMetadataType
	}

	val := reflect.New(typ).Interface()
	if len(unpacker.Metadata) > 0 {
		if err := json.Unmarshal(unpacker.Metadata, val); err != nil {
			return err
		}
	}
	metadata := reflect.ValueOf(val).Elem().Interface()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	p.Metadata = encoded
	return nil
}

type IDLikes []string

type LinuxRelease struct {
	PrettyName       string
	Name             string
	ID               string
	IDLike           IDLikes
	Version          string
	VersionID        string
	VersionCodename  string
	BuildID          string
	ImageID          string
	ImageVersion     string
	Variant          string
	VariantID        string
	HomeURL          string
	SupportURL       string
	BugReportURL     string
	PrivacyPolicyURL string
	CPEName          string
	SupportEnd       string
}

func (s *IDLikes) UnmarshalJSON(data []byte) error {
	var str string
	var strSlice []string

	// we support unmarshalling from a single value to support syft json schema v2
	if err := json.Unmarshal(data, &str); err == nil {
		*s = []string{str}
	} else if err := json.Unmarshal(data, &strSlice); err == nil {
		*s = strSlice
	} else {
		return err
	}
	return nil
}

type SyftFile struct {
	ID         string
	Location   Coordinates
	Metadata   *FileMetadataEntry
	Contents   string
	Digests    []Digest
	Licenses   []FileLicense
	Executable *Executable
}

type FileMetadataEntry struct {
	Mode            int64
	Type            string
	LinkDestination string
	UserID          int64
	GroupID         int64
	MIMEType        string
	Size_           int64
}

type FileLicense struct {
	Value          string
	SPDXExpression string
	Type           LicenseType
	Evidence       *FileLicenseEvidence
}

type Executable struct {
	// Format denotes either ELF, Mach-O, or PE
	Format ExecutableFormat

	HasExports          bool
	HasEntrypoint       bool
	ImportedLibraries   []string
	ELFSecurityFeatures *ELFSecurityFeatures
}

type ELFSecurityFeatures struct {
	SymbolTableStripped bool

	// classic protections

	StackCanary                   *bool
	NoExecutable                  bool
	RelocationReadOnly            RelocationReadOnly
	PositionIndependentExecutable bool
	DynamicSharedObject           bool

	// LlvmSafeStack represents a compiler-based security mechanism that separates the stack into a safe stack for storing return addresses and other critical data, and an unsafe stack for everything else, to mitigate stack-based memory corruption errors
	// see https://clang.llvm.org/docs/SafeStack.html
	LlvmSafeStack *bool

	// ControlFlowIntegrity represents runtime checks to ensure a program's control flow adheres to the legal paths determined at compile time, thus protecting against various types of control-flow hijacking attacks
	// see https://clang.llvm.org/docs/ControlFlowIntegrity.html
	LlvmControlFlowIntegrity *bool

	// ClangFortifySource is a broad suite of extensions to libc aimed at catching misuses of common library functions
	// see https://android.googlesource.com/platform//bionic/+/d192dbecf0b2a371eb127c0871f77a9caf81c4d2/docs/clang_fortify_anatomy.md
	ClangFortifySource *bool

	//// Selfrando provides function order shuffling to defend against ROP and other types of code reuse
	//// see https://github.com/runsafesecurity/selfrando
	// Selfrando *bool
}

type (
	ExecutableFormat   string
	RelocationReadOnly string
)

type FileLicenseEvidence struct {
	Confidence int64
	Offset     int64
	Extent     int64
}

// SyftDescriptor describes what created the document as well as surrounding metadata
type SyftDescriptor struct {
	Name          string
	Version       string
	Configuration json.RawMessage
}

type Schema struct {
	Version string
	URL     string
}

// SyftDocument represents the syft cataloging findings as a JSON document
type SyftDocument struct {
	Artifacts             []SyftPackage // Artifacts is the list of packages discovered and placed into the catalog
	ArtifactRelationships []SyftRelationship
	Files                 []SyftFile     // note: must have omitempty
	SyftSource            SyftSource     // SyftSource represents the original object that was cataloged
	Distro                LinuxRelease   // Distro represents the Linux distribution that was detected from the source
	SyftDescriptor        SyftDescriptor // SyftDescriptor is a block containing self-describing information about syft
	Schema                Schema         // Schema is a block reserved for defining the version for the shape of this JSON document and where to find the schema document to validate the shape
}
