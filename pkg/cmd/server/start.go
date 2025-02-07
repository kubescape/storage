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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"

	"github.com/didip/tollbooth/v7"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/admission/wardleinitializer"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/apiserver"
	clientset "github.com/kubescape/storage/pkg/generated/clientset/versioned"
	informers "github.com/kubescape/storage/pkg/generated/informers/externalversions"
	sampleopenapi "github.com/kubescape/storage/pkg/generated/openapi"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	netutils "k8s.io/utils/net"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const (
	defaultEtcdPathPrefix = "/registry/spdx.softwarecomposition.kubescape.io"
	defaultRateLimit      = 10000
)

// WardleServerOptions contains state for master/api server
type WardleServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions

	SharedInformerFactory informers.SharedInformerFactory
	StdOut                io.Writer
	StdErr                io.Writer

	AlternateDNS []string

	OsFs            afero.Fs
	Pool            *sqlitemigration.Pool
	Namespace       string
	WatchDispatcher *file.WatchDispatcher
}

// NewWardleServerOptions returns a new WardleServerOptions
func NewWardleServerOptions(out, errOut io.Writer, osFs afero.Fs, pool *sqlitemigration.Pool, namespace string, watchDispatcher *file.WatchDispatcher) *WardleServerOptions {
	o := &WardleServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(v1beta1.SchemeGroupVersion),
		),

		StdOut: out,
		StdErr: errOut,

		OsFs:            osFs,
		Pool:            pool,
		Namespace:       namespace,
		WatchDispatcher: watchDispatcher,
	}
	o.RecommendedOptions.Etcd = nil

	// Disable authorization since we are publishing an internal endpoint (that only answers the API server)
	o.RecommendedOptions.Authorization = nil

	// Set TLS up and bind to 8443
	value, exists := os.LookupEnv("TLS_CLIENT_CA_FILE")
	if exists {
		// Instead of reading the file contents, just set the path
		o.RecommendedOptions.Authentication.ClientCert.ClientCA = value
	} else {
		logger.L().Warning("TLS_CLIENT_CA_FILE not set")
	}
	value, exists = os.LookupEnv("TLS_SERVER_CERT_FILE")
	if exists {
		o.RecommendedOptions.SecureServing.ServerCert.CertKey.CertFile = value
	} else {
		logger.L().Warning("TLS_SERVER_CERT_FILE not set")
	}
	value, exists = os.LookupEnv("TLS_SERVER_KEY_FILE")
	if exists {
		o.RecommendedOptions.SecureServing.ServerCert.CertKey.KeyFile = value
	} else {
		logger.L().Warning("TLS_SERVER_KEY_FILE not set")
	}
	o.RecommendedOptions.SecureServing.BindPort = 8443

	return o
}

// NewCommandStartWardleServer provides a CLI handler for 'start master' command
// with a default WardleServerOptions.
func NewCommandStartWardleServer(defaults *WardleServerOptions, stopCh <-chan struct{}) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch a wardle API server",
		Long:  "Launch a wardle API server",
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
			if err := o.RunWardleServer(stopCh); err != nil {
				logger.L().Error("unable to run server validated", helpers.Error(err))
				return err
			}
			logger.L().Info("server ran")
			return nil
		},
	}

	flags := cmd.Flags()
	o.RecommendedOptions.AddFlags(flags)
	utilfeature.DefaultMutableFeatureGate.AddFlag(flags)

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
func (o WardleServerOptions) Validate(args []string) error {
	errors := []error{}
	errors = append(errors, o.RecommendedOptions.Validate()...)
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

	o.RecommendedOptions.ExtraAdmissionInitializers = func(c *genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		client, err := clientset.NewForConfig(c.LoopbackClientConfig)
		if err != nil {
			return nil, err
		}
		informerFactory := informers.NewSharedInformerFactory(client, c.LoopbackClientConfig.Timeout)
		o.SharedInformerFactory = informerFactory
		return []admission.PluginInitializer{wardleinitializer.New(informerFactory)}, nil
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = "Wardle"
	serverConfig.OpenAPIConfig.Info.Version = "0.1"

	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIV3Config.Info.Title = "Wardle"
	serverConfig.OpenAPIV3Config.Info.Version = "0.1"

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig: apiserver.ExtraConfig{
			OsFs:            o.OsFs,
			Pool:            o.Pool,
			Namespace:       o.Namespace,
			WatchDispatcher: o.WatchDispatcher,
		},
	}
	return config, nil
}

// RunWardleServer starts a new WardleServer given WardleServerOptions
func (o WardleServerOptions) RunWardleServer(stopCh <-chan struct{}) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}

	if rateLimitPerClient, err := strconv.ParseFloat(os.Getenv("RATE_LIMIT_PER_CLIENT"), 64); err == nil {
		rateLimitTotal := defaultRateLimit
		if value, err := strconv.Atoi(os.Getenv("RATE_LIMIT_TOTAL")); err == nil {
			rateLimitTotal = value
		}
		logger.L().Info("rate limiting enabled", helpers.Interface("rateLimitPerClient", rateLimitPerClient), helpers.Int("rateLimitTotal", rateLimitTotal))
		// modify fullHandlerChain to include the Tollbooth rate limiter
		fullHandlerChain := server.GenericAPIServer.Handler.FullHandlerChain
		ipLimiter := tollbooth.NewLimiter(rateLimitPerClient, nil)
		ipLimiter.SetIPLookups([]string{"X-Forwarded-For"}) // api-server acts as a reverse proxy
		globalLimiter := NewConcurrentLimiter(rateLimitTotal)
		server.GenericAPIServer.Handler.FullHandlerChain = globalLimiter.LimitConcurrentRequests(ipLimiter, fullHandlerChain.ServeHTTP)
	}

	server.GenericAPIServer.AddPostStartHookOrDie("start-sample-server-informers", func(context genericapiserver.PostStartHookContext) error {
		config.GenericConfig.SharedInformerFactory.Start(context.StopCh)
		o.SharedInformerFactory.Start(context.StopCh)
		return nil
	})

	return server.GenericAPIServer.PrepareRun().Run(stopCh)
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
