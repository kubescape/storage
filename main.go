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
	"context"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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

	if zl, err := zap.NewProduction(); err == nil {
		// klog carries the Kubernetes apiserver internals (serving, aggregation,
		// informer-sync, post-start hooks). It is muted to Fatal by default to keep
		// logs quiet, but that hides the very diagnostics needed to debug startup /
		// readyz failures. Set KS_LOGGER_LEVEL=debug to surface them.
		klogLevel := zap.FatalLevel
		if strings.EqualFold(os.Getenv("KS_LOGGER_LEVEL"), "debug") {
			klogLevel = zap.InfoLevel
		}
		zl = zl.WithOptions(zap.IncreaseLevel(klogLevel))
		klog.SetLogger(zapr.NewLogger(zl))
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
	// Under the ContainerProfile SQLite backend every legacy write goes
	// through the shared write gate from the single-writer shards, which hold
	// no pool connection while queued; with the single writer off, every REST
	// write would instead queue on the gate holding a pool connection and ten
	// queued writers would starve every reader (write-gate-sharing §3.3, §4).
	if cfg.ContainerProfileSqliteBackend && !cfg.SingleWriterEnabled {
		logger.L().Ctx(ctx).Fatal("invalid config: containerProfileSqliteBackend requires singleWriterEnabled")
	}
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
	sqlitePath := filepath.Join(file.DefaultStorageRoot, "metadata.sq3")
	pool := file.NewPoolWithOptions(sqlitePath, file.PoolOptions{
		Size:        cfg.SqlitePoolSize,
		BusyTimeout: cfg.SqliteBusyTimeout,
		// K-3: with the ContainerProfile SQLite backend on, no connection
		// checkpoints inside its own COMMIT; the backend's background PASSIVE
		// checkpointer does. Flag-off leaves SQLite's default untouched.
		DisableAutoCheckpoint: cfg.ContainerProfileSqliteBackend,
	})
	file.SetPoolTimeout(cfg.PoolTimeout)
	file.SetSingleWriterEnabled(cfg.SingleWriterEnabled)

	// The process's one write gate (.omc/plans/write-gate-sharing.md §3.1):
	// with the ContainerProfile SQLite backend on, every SQLite write of every
	// kind — the ObjectStore's, the legacy StorageImpl's and the cleanup
	// handler's — goes through it. Built beside the pool, before any writer
	// exists; closed by the apiserver's pre-shutdown hook, before Pool.Close.
	var writeGate *file.WriteGate
	if cfg.ContainerProfileSqliteBackend {
		gateCtx, gateCancel := context.WithTimeout(ctx, cfg.PoolTimeout)
		writeGate, err = file.NewWriteGate(gateCtx, pool)
		gateCancel()
		if err != nil {
			logger.L().Ctx(ctx).Fatal("write gate error", helpers.Error(err))
		}
	}

	// setup watcher
	watchDispatcher := file.NewWatchDispatcher()

	// cleanup task
	client, err := file.NewKubernetesClient()
	kubernetesAPI := file.NewKubernetesAPI(cfg, client)
	if err != nil {
		panic(err.Error())
	}

	relevancyEnabled := clusterData.RelevantImageVulnerabilitiesEnabled != nil && *clusterData.RelevantImageVulnerabilitiesEnabled

	cleanupHandler := file.NewResourcesCleanupHandler(osFs, file.DefaultStorageRoot, pool, watchDispatcher, cfg.CleanupInterval, cfg.DefaultNamespace, kubernetesAPI, relevancyEnabled)
	cleanupHandler.SetWriteGate(writeGate)
	go cleanupHandler.RunCleanupTask(ctx)

	// start the server
	options := server.NewWardleServerOptions(os.Stdout, os.Stderr, osFs, pool, cfg, watchDispatcher, cleanupHandler)
	options.SqlitePath = sqlitePath
	options.WriteGate = writeGate
	cmd := server.NewCommandStartWardleServer(ctx, options, false)
	logger.L().Info("APIServer starting")
	code := cli.Run(cmd)
	os.Exit(code)
}
