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

package main

import (
	"flag"
	"net/url"
	"os"
	"path/filepath"

	utilsmetadata "github.com/armosec/utils-k8s-go/armometadata"
	"github.com/go-logr/zapr"
	"github.com/grafana/pyroscope-go"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/cmd/server"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"go.uber.org/zap"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/component-base/cli"
	"k8s.io/klog/v2"
)

func main() {
	flag.Parse()

	if logger, err := zap.NewProduction(); err == nil {
		logger = logger.WithOptions(zap.IncreaseLevel(zap.FatalLevel))
		klog.SetLogger(zapr.NewLogger(logger))
	}

	ctx := genericapiserver.SetupSignalContext()
	clusterData, err := utilsmetadata.LoadConfig("/etc/config/clusterData.json")
	if err != nil {
		logger.L().Ctx(ctx).Fatal("load config error", helpers.Error(err))
	}
	configDir := "/etc/config"
	if envPath, present := os.LookupEnv("CONFIG_DIR"); present {
		configDir = envPath
	}
	cfg, err := config.LoadConfig(configDir)
	if err != nil {
		logger.L().Ctx(ctx).Fatal("load config error", helpers.Error(err))
	}
	cfg.DefaultNamespace = clusterData.Namespace
	// to enable otel, set OTEL_COLLECTOR_SVC=otel-collector:4317
	if otelHost, present := os.LookupEnv("OTEL_COLLECTOR_SVC"); present {
		ctx = logger.InitOtel("storage",
			os.Getenv("RELEASE"),
			clusterData.AccountID,
			clusterData.ClusterName,
			url.URL{Host: otelHost})
		defer logger.ShutdownOtel(ctx)
	}

	if pyroscopeServerSvc, present := os.LookupEnv("PYROSCOPE_SERVER_SVC"); present {
		logger.L().Info("starting pyroscope profiler")

		if os.Getenv("APPLICATION_NAME") == "" {
			os.Setenv("APPLICATION_NAME", "node-agent")
		}

		_, err := pyroscope.Start(pyroscope.Config{
			ApplicationName: os.Getenv("APPLICATION_NAME"),
			ServerAddress:   pyroscopeServerSvc,
			Logger:          pyroscope.StandardLogger,
			Tags:            map[string]string{"app": "storage", "pod": os.Getenv("POD_NAME")},
		})

		if err != nil {
			logger.L().Ctx(ctx).Error("error starting pyroscope", helpers.Error(err))
		}
	}

	// setup storage components
	osFs := afero.NewOsFs()
	pool := file.NewPool(filepath.Join(file.DefaultStorageRoot, "metadata.sq3"), 0) // If less than 1, a reasonable default is used.

	// setup watcher
	watchDispatcher := file.NewWatchDispatcher()

	// cleanup task
	client, disco, err := file.NewKubernetesClient()
	kubernetesAPI := file.NewKubernetesAPI(cfg, client, disco)
	if err != nil {
		panic(err.Error())
	}

	relevancyEnabled := clusterData.RelevantImageVulnerabilitiesEnabled != nil && *clusterData.RelevantImageVulnerabilitiesEnabled

	cleanupHandler := file.NewResourcesCleanupHandler(osFs, file.DefaultStorageRoot, pool, watchDispatcher, cfg.CleanupInterval, kubernetesAPI, relevancyEnabled)
	go cleanupHandler.RunCleanupTask(ctx)

	// start the server
	options := server.NewWardleServerOptions(os.Stdout, os.Stderr, osFs, pool, cfg, watchDispatcher, cleanupHandler)
	cmd := server.NewCommandStartWardleServer(ctx, options, false)
	logger.L().Info("APIServer starting")
	code := cli.Run(cmd)
	os.Exit(code)
}
