/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry"
	sbomregistry "github.com/kubescape/storage/pkg/registry"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/applicationprofile"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/configurationscansummary"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/containerprofile"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/generatednetworkpolicy"
	knownserver "github.com/kubescape/storage/pkg/registry/softwarecomposition/knownservers"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/networkneighborhood"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/openvulnerabilityexchange"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyftfiltereds"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyfts"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/seccompprofiles"
	vmstorage "github.com/kubescape/storage/pkg/registry/softwarecomposition/vulnerabilitymanifest"
	vmsumstorage "github.com/kubescape/storage/pkg/registry/softwarecomposition/vulnerabilitymanifestsummary"
	vsumstorage "github.com/kubescape/storage/pkg/registry/softwarecomposition/vulnerabilitysummary"
	wcsstorage "github.com/kubescape/storage/pkg/registry/softwarecomposition/workloadconfigurationscans"
	wcssumstorage "github.com/kubescape/storage/pkg/registry/softwarecomposition/workloadconfigurationscansummary"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const maxRequestBodyBytes = 1024 * 1024 * 1024

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs              = serializer.NewCodecFactory(Scheme)
	WardleComponentName = "storage"
)

func init() {
	install.Install(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig holds custom apiserver config
type ExtraConfig struct {
	CleanupHandler  *file.ResourcesCleanupHandler
	OsFs            afero.Fs
	Pool            *sqlitemigration.Pool
	StorageConfig   config.Config
	WatchDispatcher *file.WatchDispatcher
}

// Config defines the config for the apiserver
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// WardleServer contains state for a Kubernetes cluster master/api server.
type WardleServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	c := completedConfig{
		cfg.GenericConfig.Complete(),
		&cfg.ExtraConfig,
	}

	c.GenericConfig.MaxRequestBodyBytes = maxRequestBodyBytes

	c.GenericConfig.RESTOptionsGetter = &options.StorageFactoryRestOptionsFactory{
		StorageFactory: &options.SimpleStorageFactory{},
	}

	return CompletedConfig{&c}
}

// New returns a new instance of WardleServer from the given config.
func (c completedConfig) New() (*WardleServer, error) {
	genericServer, err := c.GenericConfig.New("sample-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	s := &WardleServer{
		GenericAPIServer: genericServer,
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(softwarecomposition.GroupName, Scheme, metav1.ParameterCodec, Codecs)

	var (
		storageImpl = file.NewStorageImpl(c.ExtraConfig.OsFs, file.DefaultStorageRoot, c.ExtraConfig.Pool, c.ExtraConfig.WatchDispatcher, Scheme)

		applicationProfileStorageImpl  = file.NewApplicationProfileStorage(file.NewStorageImplWithCollector(c.ExtraConfig.OsFs, file.DefaultStorageRoot, c.ExtraConfig.Pool, c.ExtraConfig.WatchDispatcher, Scheme, file.NewApplicationProfileProcessor(c.ExtraConfig.StorageConfig)))
		containerProfileStorageImpl    = file.NewStorageImplWithCollector(c.ExtraConfig.OsFs, file.DefaultStorageRoot, c.ExtraConfig.Pool, c.ExtraConfig.WatchDispatcher, Scheme, file.NewContainerProfileProcessor(c.ExtraConfig.StorageConfig, c.ExtraConfig.Pool, c.ExtraConfig.CleanupHandler))
		networkNeighborhoodStorageImpl = file.NewNetworkNeighborhoodStorage(file.NewStorageImplWithCollector(c.ExtraConfig.OsFs, file.DefaultStorageRoot, c.ExtraConfig.Pool, c.ExtraConfig.WatchDispatcher, Scheme, file.NewNetworkNeighborhoodProcessor(c.ExtraConfig.StorageConfig)))
		configScanStorageImpl          = file.NewConfigurationScanSummaryStorage(storageImpl)
		vulnerabilitySummaryStorage    = file.NewVulnerabilitySummaryStorage(storageImpl)
		generatedNetworkPolicyStorage  = file.NewGeneratedNetworkPolicyStorage(storageImpl, networkNeighborhoodStorageImpl)

		// REST endpoint registration, defaults to storageImpl.
		ep = func(f func(*runtime.Scheme, storage.Interface, generic.RESTOptionsGetter) (*registry.REST, error), s ...storage.Interface) *registry.REST {
			var si storage.Interface = storageImpl
			if len(s) > 0 {
				si = s[0]
			}
			return sbomregistry.RESTInPeace(f(Scheme, si, c.GenericConfig.RESTOptionsGetter))
		}
	)
	apiGroupInfo.VersionedResourcesStorageMap["v1beta1"] = map[string]rest.Storage{
		"applicationprofiles":                 ep(applicationprofile.NewREST, applicationProfileStorageImpl),
		"configurationscansummaries":          ep(configurationscansummary.NewREST, configScanStorageImpl),
		"containerprofiles":                   ep(containerprofile.NewREST, containerProfileStorageImpl),
		"generatednetworkpolicies":            ep(generatednetworkpolicy.NewREST, generatedNetworkPolicyStorage),
		"knownservers":                        ep(knownserver.NewREST),
		"networkneighborhoods":                ep(networkneighborhood.NewREST, networkNeighborhoodStorageImpl),
		"openvulnerabilityexchangecontainers": ep(openvulnerabilityexchange.NewREST),
		"sbomsyftfiltereds":                   ep(sbomsyftfiltereds.NewREST),
		"sbomsyfts":                           ep(sbomsyfts.NewREST),
		"seccompprofiles":                     ep(seccompprofiles.NewREST),
		"vulnerabilitymanifests":              ep(vmstorage.NewREST),
		"vulnerabilitymanifestsummaries":      ep(vmsumstorage.NewREST),
		"vulnerabilitysummaries":              ep(vsumstorage.NewREST, vulnerabilitySummaryStorage),
		"workloadconfigurationscans":          ep(wcsstorage.NewREST),
		"workloadconfigurationscansummaries":  ep(wcssumstorage.NewREST),
	}
	if c.ExtraConfig.StorageConfig.DisableVirtualCRDs {
		delete(apiGroupInfo.VersionedResourcesStorageMap["v1beta1"], "configurationscansummaries")
		delete(apiGroupInfo.VersionedResourcesStorageMap["v1beta1"], "generatednetworkpolicies")
		delete(apiGroupInfo.VersionedResourcesStorageMap["v1beta1"], "vulnerabilitysummaries")
	}

	if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	return s, nil
}
