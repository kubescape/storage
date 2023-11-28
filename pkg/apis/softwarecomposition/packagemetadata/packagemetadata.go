package packagemetadata

import (
	"reflect"
	"strings"

	"github.com/anchore/syft/syft/pkg"
	"github.com/anchore/syft/syft/source"
)

var jsonNameFromType = map[reflect.Type][]string{
	reflect.TypeOf(source.DirectorySourceMetadata{}):        {"directory", "dir"},
	reflect.TypeOf(source.FileSourceMetadata{}):             {"file"},
	reflect.TypeOf(source.StereoscopeImageSourceMetadata{}): {"image"},
}

// AllTypes returns a list of all pkg metadata types that syft supports (that are represented in the pkg.Package.Metadata field).
func AllTypes() []any {
	return []any{
		pkg.AlpmDBEntry{},
		pkg.ApkDBEntry{},
		pkg.BinarySignature{},
		pkg.CocoaPodfileLockEntry{},
		pkg.ConanLockEntry{},
		pkg.ConanfileEntry{},
		pkg.ConaninfoEntry{},
		pkg.DartPubspecLockEntry{},
		pkg.DotnetDepsEntry{},
		pkg.DotnetPortableExecutableEntry{},
		pkg.DpkgDBEntry{},
		pkg.ElixirMixLockEntry{},
		pkg.ErlangRebarLockEntry{},
		pkg.GolangBinaryBuildinfoEntry{},
		pkg.GolangModuleEntry{},
		pkg.HackageStackYamlEntry{},
		pkg.HackageStackYamlLockEntry{},
		pkg.JavaArchive{},
		pkg.LinuxKernel{},
		pkg.LinuxKernelModule{},
		pkg.MicrosoftKbPatch{},
		pkg.NixStoreEntry{},
		pkg.NpmPackage{},
		pkg.NpmPackageLockEntry{},
		pkg.PhpComposerInstalledEntry{},
		pkg.PhpComposerLockEntry{},
		pkg.PortageEntry{},
		pkg.PythonPackage{},
		pkg.PythonPipfileLockEntry{},
		pkg.PythonRequirementsEntry{},
		pkg.RDescription{},
		pkg.RpmArchive{},
		pkg.RpmDBEntry{},
		pkg.RubyGemspec{},
		pkg.RustBinaryAuditEntry{},
		pkg.RustCargoLockEntry{},
		pkg.SwiftPackageManagerResolvedEntry{},
	}
}

func AllTypeNames() []string {
	names := make([]string, 0)
	for _, t := range AllTypes() {
		names = append(names, reflect.TypeOf(t).Name())
	}
	return names
}

func JSONName(metadata any) string {
	if vs, exists := jsonNameFromType[reflect.TypeOf(metadata)]; exists {
		return vs[0]
	}
	return ""
}

func ReflectTypeFromJSONName(name string) reflect.Type {
	name = strings.ToLower(name)
	for t, vs := range jsonNameFromType {
		for _, v := range vs {
			if strings.ToLower(v) == name {
				return t
			}
		}
	}
	return nil
}
