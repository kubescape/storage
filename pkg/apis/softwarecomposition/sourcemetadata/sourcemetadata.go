package sourcemetadata

import (
	"reflect"
	"strings"

	"github.com/anchore/syft/syft/source"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
)

var jsonNameFromType = map[reflect.Type][]string{
	reflect.TypeOf(source.DirectorySourceMetadata{}):        {"directory", "dir"},
	reflect.TypeOf(source.FileSourceMetadata{}):             {"file"},
	reflect.TypeOf(source.StereoscopeImageSourceMetadata{}): {"image"},
}

// AllTypes returns a list of all source metadata types that syft supports (that are represented in the source.Description.Metadata field).
func AllTypes() []any {
	return []any{source.DirectorySourceMetadata{}, source.FileSourceMetadata{}, source.StereoscopeImageSourceMetadata{}}
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
	logger.L().Debug("dwertent ReflectTypeFromJSONName", helpers.String("name", name))
	for t, vs := range jsonNameFromType {
		for _, v := range vs {
			logger.L().Debug("dwertent", helpers.String("strings.ToLower(v)", strings.ToLower(v)))
			if strings.ToLower(v) == name {
				return t
			}
		}
	}
	return nil
}
