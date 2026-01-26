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

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/didip/tollbooth/v7"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/apiserver"
	"github.com/kubescape/storage/pkg/config"
	informers "github.com/kubescape/storage/pkg/generated/informers/externalversions"
	sampleopenapi "github.com/kubescape/storage/pkg/generated/openapi"
	"github.com/kubescape/storage/pkg/queuemanager"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/kubescape/storage/pkg/statscollector"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/util/compatibility"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	basecompatibility "k8s.io/component-base/compatibility"
	"k8s.io/component-base/featuregate"
	baseversion "k8s.io/component-base/version"
	netutils "k8s.io/utils/net"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const (
	defaultEtcdPathPrefix = "/registry/spdx.softwarecomposition.kubescape.io"
)

var stats = statscollector.NewStatsCollector()

// WardleServerOptions contains state for master/api server
type WardleServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions
	// ComponentGlobalsRegistry is the registry where the effective versions and feature gates for all components are stored.
	ComponentGlobalsRegistry basecompatibility.ComponentGlobalsRegistry

	SharedInformerFactory informers.SharedInformerFactory
	StdOut                io.Writer
	StdErr                io.Writer

	AlternateDNS []string

	CleanupHandler  *file.ResourcesCleanupHandler
	OsFs            afero.Fs
	Pool            *sqlitemigration.Pool
	StorageConfig   config.Config
	WatchDispatcher *file.WatchDispatcher
}

func WardleVersionToKubeVersion(ver *version.Version) *version.Version {
	if ver.Major() != 1 {
		return nil
	}
	kubeVer := version.MustParse(baseversion.DefaultKubeBinaryVersion)
	// "1.2" maps to kubeVer
	offset := int(ver.Minor()) - 2
	mappedVer := kubeVer.OffsetMinor(offset)
	if mappedVer.GreaterThan(kubeVer) {
		return kubeVer
	}
	return mappedVer
}

// NewWardleServerOptions returns a new WardleServerOptions
func NewWardleServerOptions(out, errOut io.Writer, osFs afero.Fs, pool *sqlitemigration.Pool, cfg config.Config, watchDispatcher *file.WatchDispatcher, cleanupHandler *file.ResourcesCleanupHandler) *WardleServerOptions {
	o := &WardleServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(v1beta1.SchemeGroupVersion),
		),
		ComponentGlobalsRegistry: compatibility.DefaultComponentGlobalsRegistry,

		StdOut: out,
		StdErr: errOut,

		CleanupHandler:  cleanupHandler,
		OsFs:            osFs,
		Pool:            pool,
		StorageConfig:   cfg,
		WatchDispatcher: watchDispatcher,
	}
	o.RecommendedOptions.Admission = nil
	o.RecommendedOptions.Etcd = nil

	// Disable authorization since we are publishing an internal endpoint (that only answers the API server)
	o.RecommendedOptions.Authorization = nil

	// Set TLS up and bind to secure port
	o.RecommendedOptions.Authentication.ClientCert.ClientCA = o.StorageConfig.TlsClientCaFile
	o.RecommendedOptions.SecureServing.ServerCert.CertKey.CertFile = o.StorageConfig.TlsServerCertFile
	o.RecommendedOptions.SecureServing.ServerCert.CertKey.KeyFile = o.StorageConfig.TlsServerKeyFile
	o.RecommendedOptions.SecureServing.BindPort = o.StorageConfig.ServerBindPort

	return o
}

// NewCommandStartWardleServer provides a CLI handler for 'start master' command
// with a default WardleServerOptions.
func NewCommandStartWardleServer(ctx context.Context, defaults *WardleServerOptions, skipDefaultComponentGlobalsRegistrySet bool) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch a wardle API server",
		Long:  "Launch a wardle API server",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if skipDefaultComponentGlobalsRegistrySet {
				return nil
			}
			return defaults.ComponentGlobalsRegistry.Set()
		},
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				logger.L().Error("config not completed", helpers.Error(err))
				return err
			}
			logger.L().Debug("config completed")
			if err := o.Validate(args); err != nil {
				logger.L().Error("config not validated", helpers.Error(err))
				return err
			}
			logger.L().Debug("config validated")
			if err := o.RunWardleServer(c.Context()); err != nil {
				logger.L().Error("unable to run server validated", helpers.Error(err))
				return err
			}
			logger.L().Info("server ran")
			return nil
		},
	}
	cmd.SetContext(ctx)

	flags := cmd.Flags()
	o.RecommendedOptions.AddFlags(flags)

	// The following lines demonstrate how to configure version compatibility and feature gates
	// for the "Wardle" component, as an example of KEP-4330.

	// Create an effective version object for the "Wardle" component.
	// This initializes the binary version, the emulation version and the minimum compatibility version.
	//
	// Note:
	// - The binary version represents the actual version of the running source code.
	// - The emulation version is the version whose capabilities are being emulated by the binary.
	// - The minimum compatibility version specifies the minimum version that the component remains compatible with.
	//
	// Refer to KEP-4330 for more details: https://github.com/kubernetes/enhancements/blob/master/keps/sig-architecture/4330-compatibility-versions
	defaultWardleVersion := "1.1"
	// Register the "Wardle" component with the global component registry,
	// associating it with its effective version and feature gate configuration.
	// Will skip if the component has been registered, like in the integration test.
	_, wardleFeatureGate := defaults.ComponentGlobalsRegistry.ComponentGlobalsOrRegister(
		apiserver.WardleComponentName, basecompatibility.NewEffectiveVersionFromString(defaultWardleVersion, "", ""),
		featuregate.NewVersionedFeatureGate(version.MustParse(defaultWardleVersion)))

	// Add versioned feature specifications for the "BanFlunder" feature.
	// These specifications, together with the effective version, determine if the feature is enabled.
	utilruntime.Must(wardleFeatureGate.AddVersioned(map[featuregate.Feature]featuregate.VersionedSpecs{
		"BanFlunder": {
			{Version: version.MustParse("1.0"), Default: false, PreRelease: featuregate.Alpha},
			{Version: version.MustParse("1.1"), Default: true, PreRelease: featuregate.Beta},
			{Version: version.MustParse("1.2"), Default: true, PreRelease: featuregate.GA, LockToDefault: true},
		},
	}))

	// Register the default kube component if not already present in the global registry.
	_, _ = defaults.ComponentGlobalsRegistry.ComponentGlobalsOrRegister(basecompatibility.DefaultKubeComponent,
		basecompatibility.NewEffectiveVersionFromString(baseversion.DefaultKubeBinaryVersion, "", ""), utilfeature.DefaultMutableFeatureGate)

	// Set the emulation version mapping from the "Wardle" component to the kube component.
	// This ensures that the emulation version of the latter is determined by the emulation version of the former.
	utilruntime.Must(defaults.ComponentGlobalsRegistry.SetVersionMapping(apiserver.WardleComponentName, basecompatibility.DefaultKubeComponent, WardleVersionToKubeVersion))

	defaults.ComponentGlobalsRegistry.AddFlags(flags)

	// replace built-in profiling with pprof on port 6060
	err := flags.Set("profiling", "false")
	if err != nil {
		logger.L().Warning("failed to set profiling flag to false", helpers.Error(err))
	}
	servePprof()

	// mute klog
	// https://github.com/kubernetes/klog/issues/87
	// klog.SetLogger(logr.Discard())

	logger.L().Info("starting storage server")

	return cmd
}

// Validate validates WardleServerOptions
func (o WardleServerOptions) Validate(_ []string) error {
	var errors []error
	errors = append(errors, o.RecommendedOptions.Validate()...)
	errors = append(errors, o.ComponentGlobalsRegistry.Validate()...)
	return utilerrors.NewAggregate(errors)
}

// Complete fills in fields required to have valid data
func (o *WardleServerOptions) Complete() error {

	return nil
}

// Config returns config for the api server given WardleServerOptions
func (o *WardleServerOptions) Config() (*apiserver.Config, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", o.AlternateDNS, []net.IP{netutils.ParseIPSloppy("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = "Wardle"
	serverConfig.OpenAPIConfig.Info.Version = "0.1"

	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIV3Config.Info.Title = "Wardle"
	serverConfig.OpenAPIV3Config.Info.Version = "0.1"

	serverConfig.FeatureGate = o.ComponentGlobalsRegistry.FeatureGateFor(basecompatibility.DefaultKubeComponent)
	serverConfig.EffectiveVersion = o.ComponentGlobalsRegistry.EffectiveVersionFor(apiserver.WardleComponentName)

	serverConfig.BuildHandlerChainFunc = func(apiHandler http.Handler, c *genericapiserver.Config) http.Handler {
		handler := genericapiserver.DefaultBuildHandlerChain(apiHandler, c) // Default handler chain
		if o.StorageConfig.QueueProcessingStatsPrint {
			handler = stats.Handler(handler) // Attach stats collector
		}
		if o.StorageConfig.QueueTimeoutPrint {
			handler = queuemanager.TimeoutLoggerMiddleware(handler, o.StorageConfig.QueueTimeout) // Attach timeout logger
		}
		if o.StorageConfig.QueueManagerEnabled {
			queueManager := queuemanager.NewQueueManager(&o.StorageConfig) // Attach queue manager
			handler = queueManager.QueueHandler(handler)                   // Attach queue manager
		}
		return handler
	}

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	c := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig: apiserver.ExtraConfig{
			CleanupHandler:  o.CleanupHandler,
			OsFs:            o.OsFs,
			Pool:            o.Pool,
			StorageConfig:   o.StorageConfig,
			WatchDispatcher: o.WatchDispatcher,
		},
	}
	return c, nil
}

// RunWardleServer starts a new WardleServer given WardleServerOptions
func (o WardleServerOptions) RunWardleServer(ctx context.Context) error {
	c, err := o.Config()
	if err != nil {
		return err
	}

	server, err := c.Complete().New()
	if err != nil {
		return err
	}

	rateLimitPerClient := c.ExtraConfig.StorageConfig.RateLimitPerClient
	if rateLimitPerClient > 0 {
		rateLimitTotal := c.ExtraConfig.StorageConfig.RateLimitTotal
		logger.L().Info("rate limiting enabled", helpers.Interface("rateLimitPerClient", rateLimitPerClient), helpers.Int("rateLimitTotal", rateLimitTotal))
		// modify fullHandlerChain to include the Tollbooth rate limiter
		fullHandlerChain := server.GenericAPIServer.Handler.FullHandlerChain
		ipLimiter := tollbooth.NewLimiter(rateLimitPerClient, nil)
		ipLimiter.SetIPLookups([]string{"X-Forwarded-For"}) // api-server acts as a reverse proxy
		globalLimiter := NewConcurrentLimiter(rateLimitTotal)
		server.GenericAPIServer.Handler.FullHandlerChain = globalLimiter.LimitConcurrentRequests(ipLimiter, fullHandlerChain.ServeHTTP)
	}

	if o.StorageConfig.QueueProcessingStatsPrint {
		go func() {
			for {
				time.Sleep(5 * time.Minute)
				currentStats := stats.GetStats(true)
				for kind, verbs := range currentStats {
					for verb, stats := range verbs {
						logger.L().Info("stats", helpers.String("kind", kind),
							helpers.String("verb", verb), helpers.Int("count", int(stats.Count)),
							helpers.String("min", stats.Min.String()), helpers.String("max", stats.Max.String()),
							helpers.String("avg", time.Duration(stats.Sum.Nanoseconds()/stats.Count).String()))
					}
				}
			}
		}()
	}

	return server.GenericAPIServer.PrepareRun().RunWithContext(ctx)
}

func servePprof() {
	if logger.L().GetLevel() == helpers.DebugLevel.String() {
		logger.L().Info("starting pprof server", helpers.String("port", "6060"))
		pprofMux := http.NewServeMux()
		pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
		pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pprofMux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		pprofMux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		pprofMux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
		pprofMux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
		pprofMux.Handle("/debug/pprof/block", pprof.Handler("block"))
		pprofMux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		go func() {
			if err := http.ListenAndServe(":6060", pprofMux); err != nil {
				logger.L().Error("failed to start pprof server", helpers.Error(err))
			}
		}()
	}
}
