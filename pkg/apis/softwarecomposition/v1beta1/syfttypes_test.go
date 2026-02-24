package v1beta1

import (
	"testing"

	syftartifacts "github.com/anchore/syft/syft/artifact"
	syftfile "github.com/anchore/syft/syft/file"
	"github.com/anchore/syft/syft/license"
	"github.com/anchore/syft/syft/pkg"
	"github.com/anchore/syft/syft/sbom"
	"github.com/anchore/syft/syft/source"
	"github.com/stretchr/testify/assert"
)

func TestStripSBOM(t *testing.T) {
	tests := []struct {
		name   string
		input  *sbom.SBOM
		verify func(*testing.T, *sbom.SBOM)
	}{
		{
			name:  "nil SBOM",
			input: nil,
			verify: func(t *testing.T, sbom *sbom.SBOM) {
				assert.Nil(t, sbom, "should not panic with nil input")
			},
		},
		{
			name:  "nil packages collection",
			input: createSBOMWithNilPackages(),
			verify: func(t *testing.T, sbom *sbom.SBOM) {
				assert.Nil(t, sbom.Artifacts.Packages, "packages should remain nil")
				assert.Nil(t, sbom.Descriptor.Configuration, "descriptor configuration should be cleared")
				assert.NotNil(t, sbom.Artifacts.FileMetadata, "file metadata should be preserved")
				assert.NotNil(t, sbom.Artifacts.FileDigests, "file digests should be preserved")
				assert.NotNil(t, sbom.Artifacts.FileContents, "file contents should be preserved")
				assert.Nil(t, sbom.Artifacts.FileLicenses, "file licenses should be cleared")
				assert.Nil(t, sbom.Artifacts.Executables, "executables should be cleared")
				assert.NotNil(t, sbom.Artifacts.Unknowns, "unknowns should be preserved")
				assert.Nil(t, sbom.Relationships, "relationships should be cleared")
			},
		},
		{
			name:  "complete SBOM",
			input: createCompleteSBOM(),
			verify: func(t *testing.T, sbom *sbom.SBOM) {
				assert.Nil(t, sbom.Descriptor.Configuration, "descriptor configuration should be cleared")
				assert.NotNil(t, sbom.Artifacts.FileMetadata, "file metadata should be preserved")
				assert.NotNil(t, sbom.Artifacts.FileDigests, "file digests should be preserved")
				assert.NotNil(t, sbom.Artifacts.FileContents, "file contents should be preserved")
				assert.Nil(t, sbom.Artifacts.FileLicenses, "file licenses should be cleared")
				assert.Nil(t, sbom.Artifacts.Executables, "executables should be cleared")
				assert.NotNil(t, sbom.Artifacts.Unknowns, "unknowns should be preserved")
				assert.Nil(t, sbom.Relationships, "relationships should be cleared")

				for p := range sbom.Artifacts.Packages.Enumerate() {
					assert.Equal(t, "", p.FoundBy, "package FoundBy should be cleared")
					assert.IsType(t, pkg.ApkDBEntry{}, p.Metadata, "package Metadata should be ApkDBEntry type for ApkPkg")

					apkMetadata, ok := p.Metadata.(pkg.ApkDBEntry)
					assert.True(t, ok, "Metadata should be castable to ApkDBEntry")
					assert.Equal(t, "openssl", apkMetadata.OriginPackage, "OriginPackage should be preserved")
					assert.Equal(t, "", apkMetadata.Package, "Package should be cleared")
					assert.Equal(t, "", apkMetadata.Architecture, "Architecture should be cleared")

					licenses := p.Licenses.ToSlice()
					for _, lic := range licenses {
						assert.Empty(t, lic.Locations.ToSlice(), "license locations should be cleared")
					}

					locations := p.Locations.ToSlice()
					for _, loc := range locations {
						assert.Equal(t, "", loc.AccessPath, "location AccessPath should be cleared")
						assert.Nil(t, loc.Annotations, "location Annotations should be cleared")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			StripSBOM(tt.input)
			tt.verify(t, tt.input)
		})
	}
}

func createSBOMWithNilPackages() *sbom.SBOM {
	return &sbom.SBOM{
		Source: source.Description{
			Metadata: "test metadata",
		},
		Descriptor: sbom.Descriptor{
			Configuration: "test configuration",
		},
		Artifacts: sbom.Artifacts{
			Packages:     nil,
			FileMetadata: map[syftfile.Coordinates]syftfile.Metadata{},
			FileDigests:  map[syftfile.Coordinates][]syftfile.Digest{},
			FileContents: map[syftfile.Coordinates]string{},
			FileLicenses: map[syftfile.Coordinates][]syftfile.License{},
			Executables:  map[syftfile.Coordinates]syftfile.Executable{},
			Unknowns:     map[syftfile.Coordinates][]string{},
		},
		Relationships: []syftartifacts.Relationship{{}},
	}
}

func createCompleteSBOM() *sbom.SBOM {
	testPackage := createTestPackage()

	return &sbom.SBOM{
		Source: source.Description{
			Metadata: "test source metadata",
		},
		Descriptor: sbom.Descriptor{
			Name:          "test-descriptor",
			Version:       "1.0.0",
			Configuration: "test configuration",
		},
		Artifacts: sbom.Artifacts{
			Packages:     pkg.NewCollection(testPackage),
			FileMetadata: map[syftfile.Coordinates]syftfile.Metadata{},
			FileDigests:  map[syftfile.Coordinates][]syftfile.Digest{},
			FileContents: map[syftfile.Coordinates]string{},
			FileLicenses: map[syftfile.Coordinates][]syftfile.License{},
			Executables:  map[syftfile.Coordinates]syftfile.Executable{},
			Unknowns:     map[syftfile.Coordinates][]string{},
		},
		Relationships: []syftartifacts.Relationship{{}},
	}
}

func createTestPackage() pkg.Package {
	testLicense := pkg.License{
		Value:          "MIT",
		SPDXExpression: "MIT",
		Type:           license.Declared,
		Locations: syftfile.NewLocationSet(
			syftfile.NewVirtualLocation(
				"/path/to/license",
				"/virtual/path/to/license",
			).WithAnnotation("key", "value"),
		),
	}

	testLocation := syftfile.NewVirtualLocation(
		"/path/to/package",
		"/virtual/path/to/package",
	).WithAnnotation("virtual", "annotation")

	apkMetadata := pkg.ApkDBEntry{
		Package:       "test-package",
		OriginPackage: "openssl",
		Architecture:  "x86_64",
	}

	result := pkg.Package{
		Name:      "test-package",
		Version:   "1.2.3",
		Type:      pkg.ApkPkg,
		FoundBy:   "test-cataloger",
		Licenses:  pkg.NewLicenseSet(testLicense),
		Locations: syftfile.NewLocationSet(testLocation),
		Metadata:  apkMetadata,
	}
	result.SetID()
	return result
}
