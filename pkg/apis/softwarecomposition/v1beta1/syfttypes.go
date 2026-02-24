package v1beta1

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	syftfile "github.com/anchore/syft/syft/file"
	"github.com/anchore/syft/syft/license"
	"github.com/anchore/syft/syft/pkg"
	"github.com/anchore/syft/syft/sbom"
	"github.com/anchore/syft/syft/source"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/packagemetadata"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/sourcemetadata"
)

type Digest struct {
	Algorithm string `json:"algorithm" protobuf:"bytes,1,req,name=algorithm"`
	Value     string `json:"value" protobuf:"bytes,2,req,name=value"`
}

type LocationMetadata struct {
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,1,opt,name=annotations"` // Arbitrary key-value pairs that can be used to annotate a location
}

// Coordinates contains the minimal information needed to describe how to find a file within any possible source object (e.g. image and directory sources)
type Coordinates struct {
	RealPath     string `json:"path" cyclonedx:"path" protobuf:"bytes,1,req,name=path"`                    // The path where all path ancestors have no hardlinks / symlinks
	FileSystemID string `json:"layerID,omitempty" cyclonedx:"layerID" protobuf:"bytes,2,opt,name=layerID"` // An ID representing the filesystem. For container images, this is a layer digest. For directories or a root filesystem, this is blank.
}

// Location represents a path relative to a particular filesystem resolved to a specific file.Reference. This struct is used as a key
// in content fetching to uniquely identify a file relative to a request (the VirtualPath).
type Location struct {
	LocationData     `cyclonedx:"" protobuf:"bytes,1,opt,name=locationData"`
	LocationMetadata `cyclonedx:"" protobuf:"bytes,2,opt,name=locationMetadata"`
}

type LocationData struct {
	Coordinates `cyclonedx:"" protobuf:"bytes,1,opt,name=coordinates"` // Empty string here means there is no intermediate property name, e.g. syft:locations:0:path without "coordinates"
	// note: it is IMPORTANT to ignore anything but the coordinates for a Location when considering the ID (hash value)
	// since the coordinates are the minimally correct ID for a location (symlinks should not come into play)
	VirtualPath string `hash:"ignore" json:"accessPath" protobuf:"bytes,2,req,name=accessPath"` // The path to the file which may or may not have hardlinks / symlinks
}

// SyftSource object represents the thing that was cataloged
type SyftSource struct {
	ID       string          `json:"id" protobuf:"bytes,1,req,name=id"`
	Name     string          `json:"name" protobuf:"bytes,2,req,name=name"`
	Version  string          `json:"version" protobuf:"bytes,3,req,name=version"`
	Type     string          `json:"type" protobuf:"bytes,4,req,name=type"`
	Metadata json.RawMessage `json:"metadata" protobuf:"bytes,5,req,name=metadata"`
}

// sourceUnpacker is used to unmarshal SyftSource objects
type sourceUnpacker struct {
	ID       string          `json:"id,omitempty" protobuf:"bytes,1,opt,name=id"`
	Name     string          `json:"name" protobuf:"bytes,2,req,name=name"`
	Version  string          `json:"version" protobuf:"bytes,3,req,name=version"`
	Type     string          `json:"type" protobuf:"bytes,4,req,name=type"`
	Metadata json.RawMessage `json:"metadata" protobuf:"bytes,5,req,name=metadata"`
	Target   json.RawMessage `json:"target" protobuf:"bytes,6,req,name=target"` // pre-v9 schema support
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
	Parent   string          `json:"parent" protobuf:"bytes,1,req,name=parent"`
	Child    string          `json:"child" protobuf:"bytes,2,req,name=child"`
	Type     string          `json:"type" protobuf:"bytes,3,req,name=type"`
	Metadata json.RawMessage `json:"metadata,omitempty" protobuf:"bytes,4,opt,name=metadata"`
}

// SyftPackage represents a pkg.SyftPackage object specialized for JSON marshaling and unmarshalling.
type SyftPackage struct {
	PackageBasicData  `protobuf:"bytes,1,opt,name=packageBasicData"`
	PackageCustomData `protobuf:"bytes,2,opt,name=packageCustomData"`
}

// PackageBasicData contains non-ambiguous values (type-wise) from pkg.SyftPackage.
type PackageBasicData struct {
	ID        string     `json:"id" protobuf:"bytes,1,req,name=id"`
	Name      string     `json:"name" protobuf:"bytes,2,req,name=name"`
	Version   string     `json:"version" protobuf:"bytes,3,req,name=version"`
	Type      string     `json:"type" protobuf:"bytes,4,req,name=type"`
	FoundBy   string     `json:"foundBy" protobuf:"bytes,5,req,name=foundBy"`
	Locations []Location `json:"locations" protobuf:"bytes,6,rep,name=locations"`
	Licenses  Licenses   `json:"licenses" protobuf:"bytes,7,rep,name=licenses"`
	Language  string     `json:"language" protobuf:"bytes,8,req,name=language"`
	CPEs      CPEs       `json:"cpes" protobuf:"bytes,9,rep,name=cpes"`
	PURL      string     `json:"purl" protobuf:"bytes,10,req,name=purl"`
}

// PackageBasicDataV01011 is the previous version of PackageBasicData used in schema v0.101.1.
type PackageBasicDataV01011 struct {
	ID        string     `json:"id" protobuf:"bytes,1,req,name=id"`
	Name      string     `json:"name" protobuf:"bytes,2,req,name=name"`
	Version   string     `json:"version" protobuf:"bytes,3,req,name=version"`
	Type      string     `json:"type" protobuf:"bytes,4,req,name=type"`
	FoundBy   string     `json:"foundBy" protobuf:"bytes,5,req,name=foundBy"`
	Locations []Location `json:"locations" protobuf:"bytes,6,rep,name=locations"`
	Licenses  Licenses   `json:"licenses" protobuf:"bytes,7,rep,name=licenses"`
	Language  string     `json:"language" protobuf:"bytes,8,req,name=language"`
	CPEs      []string   `json:"cpes" protobuf:"bytes,9,rep,name=cpes"`
	PURL      string     `json:"purl" protobuf:"bytes,10,req,name=purl"`
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
	Value  string `json:"cpe" protobuf:"bytes,1,req,name=cpe"`
	Source string `json:"source,omitempty" protobuf:"bytes,2,opt,name=source"`
}

type LicenseType string

type Licenses []License

type License struct {
	Value          string      `json:"value" protobuf:"bytes,1,req,name=value"`
	SPDXExpression string      `json:"spdxExpression" protobuf:"bytes,2,req,name=spdxExpression"`
	Type           LicenseType `json:"type" protobuf:"bytes,3,req,name=type"`
	URLs           []string    `json:"urls" protobuf:"bytes,4,rep,name=urls"`
	Locations      []Location  `json:"locations" protobuf:"bytes,5,rep,name=locations"`
}

func newModelLicensesFromValues(licenses []string) (ml []License) {
	for _, v := range licenses {
		expression, _ := license.ParseExpression(v)
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
	MetadataType string          `json:"metadataType,omitempty" protobuf:"bytes,1,opt,name=metadataType"`
	Metadata     json.RawMessage `json:"metadata,omitempty" protobuf:"bytes,2,opt,name=metadata"`
}

// packageMetadataUnpacker is all values needed from SyftPackage to disambiguate ambiguous fields during json unmarshaling.
type packageMetadataUnpacker struct {
	MetadataType string          `json:"metadataType" protobuf:"bytes,1,req,name=metadataType"`
	Metadata     json.RawMessage `json:"metadata" protobuf:"bytes,2,req,name=metadata"`
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
		return err
	}

	err := unpackPkgMetadata(p, unpacker)
	if errors.Is(err, errUnknownMetadataType) {
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
	PrettyName       string  `json:"prettyName,omitempty" protobuf:"bytes,1,opt,name=prettyName"`
	Name             string  `json:"name,omitempty" protobuf:"bytes,2,opt,name=name"`
	ID               string  `json:"id,omitempty" protobuf:"bytes,3,opt,name=id"`
	IDLike           IDLikes `json:"idLike,omitempty" protobuf:"bytes,4,opt,name=idLike"`
	Version          string  `json:"version,omitempty" protobuf:"bytes,5,opt,name=version"`
	VersionID        string  `json:"versionID,omitempty" protobuf:"bytes,6,opt,name=versionID"`
	VersionCodename  string  `json:"versionCodename,omitempty" protobuf:"bytes,7,opt,name=versionCodename"`
	BuildID          string  `json:"buildID,omitempty" protobuf:"bytes,8,opt,name=buildID"`
	ImageID          string  `json:"imageID,omitempty" protobuf:"bytes,9,opt,name=imageID"`
	ImageVersion     string  `json:"imageVersion,omitempty" protobuf:"bytes,10,opt,name=imageVersion"`
	Variant          string  `json:"variant,omitempty" protobuf:"bytes,11,opt,name=variant"`
	VariantID        string  `json:"variantID,omitempty" protobuf:"bytes,12,opt,name=variantID"`
	HomeURL          string  `json:"homeURL,omitempty" protobuf:"bytes,13,opt,name=homeURL"`
	SupportURL       string  `json:"supportURL,omitempty" protobuf:"bytes,14,opt,name=supportURL"`
	BugReportURL     string  `json:"bugReportURL,omitempty" protobuf:"bytes,15,opt,name=bugReportURL"`
	PrivacyPolicyURL string  `json:"privacyPolicyURL,omitempty" protobuf:"bytes,16,opt,name=privacyPolicyURL"`
	CPEName          string  `json:"cpeName,omitempty" protobuf:"bytes,17,opt,name=cpeName"`
	SupportEnd       string  `json:"supportEnd,omitempty" protobuf:"bytes,18,opt,name=supportEnd"`
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
	ID         string             `json:"id" protobuf:"bytes,1,req,name=id"`
	Location   Coordinates        `json:"location" protobuf:"bytes,2,req,name=location"`
	Metadata   *FileMetadataEntry `json:"metadata,omitempty" protobuf:"bytes,3,opt,name=metadata"`
	Contents   string             `json:"contents,omitempty" protobuf:"bytes,4,opt,name=contents"`
	Digests    []Digest           `json:"digests,omitempty" protobuf:"bytes,5,rep,name=digests"`
	Licenses   []FileLicense      `json:"licenses,omitempty" protobuf:"bytes,6,rep,name=licenses"`
	Executable *Executable        `json:"executable,omitempty" protobuf:"bytes,7,opt,name=executable"`
}

type FileMetadataEntry struct {
	Mode            int64  `json:"mode" protobuf:"bytes,1,req,name=mode"`
	Type            string `json:"type" protobuf:"bytes,2,req,name=type"`
	LinkDestination string `json:"linkDestination,omitempty" protobuf:"bytes,3,opt,name=linkDestination"`
	UserID          int64  `json:"userID" protobuf:"bytes,4,req,name=userID"`
	GroupID         int64  `json:"groupID" protobuf:"bytes,5,req,name=groupID"`
	MIMEType        string `json:"mimeType" protobuf:"bytes,6,req,name=mimeType"`
	Size_           int64  `json:"size" protobuf:"bytes,7,req,name=size"`
}

type FileLicense struct {
	Value          string               `json:"value" protobuf:"bytes,1,req,name=value"`
	SPDXExpression string               `json:"spdxExpression" protobuf:"bytes,2,req,name=spdxExpression"`
	Type           LicenseType          `json:"type" protobuf:"bytes,3,req,name=type"`
	Evidence       *FileLicenseEvidence `json:"evidence,omitempty" protobuf:"bytes,4,opt,name=evidence"`
}

type Executable struct {
	// Format denotes either ELF, Mach-O, or PE
	Format ExecutableFormat `json:"format" yaml:"format" mapstructure:"format" protobuf:"bytes,1,req,name=format"`

	HasExports          bool                 `json:"hasExports" yaml:"hasExports" mapstructure:"hasExports" protobuf:"bytes,2,req,name=hasExports"`
	HasEntrypoint       bool                 `json:"hasEntrypoint" yaml:"hasEntrypoint" mapstructure:"hasEntrypoint" protobuf:"bytes,3,req,name=hasEntrypoint"`
	ImportedLibraries   []string             `json:"importedLibraries" yaml:"importedLibraries" mapstructure:"importedLibraries" protobuf:"bytes,4,rep,name=importedLibraries"`
	ELFSecurityFeatures *ELFSecurityFeatures `json:"elfSecurityFeatures,omitempty" yaml:"elfSecurityFeatures" mapstructure:"elfSecurityFeatures" protobuf:"bytes,5,opt,name=elfSecurityFeatures"`
}

type ELFSecurityFeatures struct {
	SymbolTableStripped bool `json:"symbolTableStripped" yaml:"symbolTableStripped" mapstructure:"symbolTableStripped" protobuf:"bytes,1,req,name=symbolTableStripped"`

	// classic protections

	StackCanary                   *bool              `json:"stackCanary,omitempty" yaml:"stackCanary" mapstructure:"stackCanary" protobuf:"bytes,2,opt,name=stackCanary"`
	NoExecutable                  bool               `json:"nx" yaml:"nx" mapstructure:"nx" protobuf:"bytes,3,req,name=nx"`
	RelocationReadOnly            RelocationReadOnly `json:"relRO" yaml:"relRO" mapstructure:"relRO" protobuf:"bytes,4,req,name=relRO"`
	PositionIndependentExecutable bool               `json:"pie" yaml:"pie" mapstructure:"pie" protobuf:"bytes,5,req,name=pie"`
	DynamicSharedObject           bool               `json:"dso" yaml:"dso" mapstructure:"dso" protobuf:"bytes,6,req,name=dso"`

	// LlvmSafeStack represents a compiler-based security mechanism that separates the stack into a safe stack for storing return addresses and other critical data, and an unsafe stack for everything else, to mitigate stack-based memory corruption errors
	// see https://clang.llvm.org/docs/SafeStack.html
	LlvmSafeStack *bool `json:"safeStack,omitempty" yaml:"safeStack" mapstructure:"safeStack" protobuf:"bytes,7,opt,name=safeStack"`

	// ControlFlowIntegrity represents runtime checks to ensure a program's control flow adheres to the legal paths determined at compile time, thus protecting against various types of control-flow hijacking attacks
	// see https://clang.llvm.org/docs/ControlFlowIntegrity.html
	LlvmControlFlowIntegrity *bool `json:"cfi,omitempty" yaml:"cfi" mapstructure:"cfi" protobuf:"bytes,8,opt,name=cfi"`

	// ClangFortifySource is a broad suite of extensions to libc aimed at catching misuses of common library functions
	// see https://android.googlesource.com/platform//bionic/+/d192dbecf0b2a371eb127c0871f77a9caf81c4d2/docs/clang_fortify_anatomy.md
	ClangFortifySource *bool `json:"fortify,omitempty" yaml:"fortify" mapstructure:"fortify" protobuf:"bytes,9,opt,name=fortify"`

	//// Selfrando provides function order shuffling to defend against ROP and other types of code reuse
	//// see https://github.com/runsafesecurity/selfrando
	// Selfrando *bool `json:"selfrando,omitempty" yaml:"selfrando" mapstructure:"selfrando"`
}

type (
	ExecutableFormat   string
	RelocationReadOnly string
)

type FileLicenseEvidence struct {
	Confidence int64 `json:"confidence" protobuf:"bytes,1,req,name=confidence"`
	Offset     int64 `json:"offset" protobuf:"bytes,2,req,name=offset"`
	Extent     int64 `json:"extent" protobuf:"bytes,3,req,name=extent"`
}

// SyftDescriptor describes what created the document as well as surrounding metadata
type SyftDescriptor struct {
	Name          string          `json:"name" protobuf:"bytes,1,req,name=name"`
	Version       string          `json:"version" protobuf:"bytes,2,req,name=version"`
	Configuration json.RawMessage `json:"configuration,omitempty" protobuf:"bytes,3,opt,name=configuration"`
}

type Schema struct {
	Version string `json:"version" protobuf:"bytes,1,req,name=version"`
	URL     string `json:"url" protobuf:"bytes,2,req,name=url"`
}

// SyftDocument represents the syft cataloging findings as a JSON document
type SyftDocument struct {
	Artifacts             []SyftPackage      `json:"artifacts" protobuf:"bytes,1,rep,name=artifacts"` // Artifacts is the list of packages discovered and placed into the catalog
	ArtifactRelationships []SyftRelationship `json:"artifactRelationships" protobuf:"bytes,2,rep,name=artifactRelationships"`
	Files                 []SyftFile         `json:"files,omitempty" protobuf:"bytes,3,rep,name=files"` // note: must have omitempty
	SyftSource            SyftSource         `json:"source" protobuf:"bytes,4,req,name=source"`         // SyftSource represents the original object that was cataloged
	Distro                LinuxRelease       `json:"distro" protobuf:"bytes,5,req,name=distro"`         // Distro represents the Linux distribution that was detected from the source
	SyftDescriptor        SyftDescriptor     `json:"descriptor" protobuf:"bytes,6,req,name=descriptor"` // SyftDescriptor is a block containing self-describing information about syft
	Schema                Schema             `json:"schema" protobuf:"bytes,7,req,name=schema"`         // Schema is a block reserved for defining the version for the shape of this JSON document and where to find the schema document to validate the shape
}

// StripSBOM removes unnecessary fields from a Syft SBOM to reduce size
func StripSBOM(syftSBOM *sbom.SBOM) {
	if syftSBOM == nil {
		return
	}

	// Clear descriptor configuration
	syftSBOM.Descriptor.Configuration = nil

	// Clear file-level artifact maps
	// Note: we have to keep FileMetadata, FileDigests, FileContents, Unknowns as they are used to create "files"
	syftSBOM.Artifacts.FileLicenses = nil
	syftSBOM.Artifacts.Executables = nil

	if syftSBOM.Artifacts.Packages == nil {
		return
	}

	// Clear fields in each artifact by rebuilding the collection
	var modifiedPackages []pkg.Package
	for p := range syftSBOM.Artifacts.Packages.Enumerate() {
		p.FoundBy = ""
		// Preserve only the fields needed by vulnerability scanners (e.g. Grype).
		// Everything else is cleared to reduce size.
		switch meta := p.Metadata.(type) {
		case pkg.ApkDBEntry:
			p.Metadata = pkg.ApkDBEntry{OriginPackage: meta.OriginPackage}
		case pkg.DpkgDBEntry:
			p.Metadata = pkg.DpkgDBEntry{Source: meta.Source, SourceVersion: meta.SourceVersion}
		case pkg.DpkgArchiveEntry:
			p.Metadata = pkg.DpkgArchiveEntry{Source: meta.Source, SourceVersion: meta.SourceVersion}
		case pkg.RpmDBEntry:
			p.Metadata = pkg.RpmDBEntry{SourceRpm: meta.SourceRpm, Epoch: meta.Epoch, ModularityLabel: meta.ModularityLabel}
		case pkg.RpmArchive:
			p.Metadata = pkg.RpmArchive{SourceRpm: meta.SourceRpm, Epoch: meta.Epoch, ModularityLabel: meta.ModularityLabel}
		case pkg.GolangBinaryBuildinfoEntry:
			// MainModule is read by the Go matcher to avoid false-positive matching on the
			// binary's own embedded main module when its version is "(devel)"
			p.Metadata = pkg.GolangBinaryBuildinfoEntry{MainModule: meta.MainModule}
		case pkg.JavaArchive:
			// PomProperties (GroupID/ArtifactID), Manifest.Main (Name key), VirtualPath and
			// ArchiveDigests are all used by Grype for Java CVE lookup and Maven fallback.
			// PomProject and Manifest.Sections are presentation-only and stripped.
			var manifest *pkg.JavaManifest
			if meta.Manifest != nil {
				manifest = &pkg.JavaManifest{Main: meta.Manifest.Main}
			}
			p.Metadata = pkg.JavaArchive{
				VirtualPath:    meta.VirtualPath,
				Manifest:       manifest,
				PomProperties:  meta.PomProperties,
				ArchiveDigests: meta.ArchiveDigests,
			}
		case pkg.JavaVMInstallation:
			p.Metadata = pkg.JavaVMInstallation{
				Release: pkg.JavaVMRelease{
					JavaRuntimeVersion: meta.Release.JavaRuntimeVersion,
					JavaVersion:        meta.Release.JavaVersion,
					FullVersion:        meta.Release.FullVersion,
					SemanticVersion:    meta.Release.SemanticVersion,
				},
			}
		default:
			p.Metadata = nil
		}

		// Clear license locations by rebuilding the license set
		licenses := p.Licenses.ToSlice()
		var modifiedLicenses []pkg.License
		for _, lic := range licenses {
			lic.Locations = syftfile.NewLocationSet()
			modifiedLicenses = append(modifiedLicenses, lic)
		}
		p.Licenses = pkg.NewLicenseSet(modifiedLicenses...)

		// Clear virtual path in locations by rebuilding the location set
		locations := p.Locations.ToSlice()
		var modifiedLocations []syftfile.Location
		for _, loc := range locations {
			loc.AccessPath = ""
			loc.Annotations = nil
			modifiedLocations = append(modifiedLocations, loc)
		}
		p.Locations = syftfile.NewLocationSet(modifiedLocations...)

		modifiedPackages = append(modifiedPackages, p)
	}

	// Replace the collection with modified packages
	syftSBOM.Artifacts.Packages = pkg.NewCollection(modifiedPackages...)
}
