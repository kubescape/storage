package cleanup

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"testing"

	_ "embed"

	sets "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

//go:embed testdata/imageids.json
var imageIds []byte

//go:embed testdata/instanceids.json
var instanceIds []byte

//go:embed testdata/wlids.json
var wlids []byte

func TestCleanupTask(t *testing.T) {
	memFs := afero.NewMemMapFs()
	// extract test data
	err := unzipSource("./testdata/data.zip", memFs)
	if err != nil {
		t.Fatal(err)
	}

	handler := NewResourcesCleanupHandler(memFs, file.DefaultStorageRoot, time.Hour*0, &ResourcesFetchMock{})
	handler.StartCleanupTask()

	expectedFilesToDelete := []string{
		"/data/spdx.softwarecomposition.kubescape.io/applicationactivities/gadget/gadget-daemonset-gadget-0d7c-fd3c.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationactivities/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofiles/gadget/gadget-daemonset-gadget-0d7c-fd3c.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofiles/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/default/default-replicaset-nginx-748c667d99-cf81-0278.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/default/default-replicaset-nginx-748c667d99-cf81-0278.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/gadget/gadget-daemonset-gadget-0d7c-fd3c.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-gateway-798c4c5f44-b8b1-1308.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-gateway-798c4c5f44-b8b1-1308.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-kubescape-6cff94799d-8110-156a.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-kubescape-6cff94799d-8110-156a.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-operator-575cf58d76-4ad4-39ec.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-operator-575cf58d76-4ad4-39ec.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-otel-collector-54648b7dbb-a539-eb0b.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-otel-collector-54648b7dbb-a539-eb0b.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-storage-8f57967d7-d272-b1f5.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-storage-8f57967d7-d272-b1f5.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-synchronizer-79b57d5d67-6912-e9a6.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-replicaset-synchronizer-79b57d5d67-6912-e9a6.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-statefulset-kollector-c1be-77d8.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/kubescape/kubescape-statefulset-kollector-c1be-77d8.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/local-path-storage/local-path-storage-replicaset-local-path-provisioner-75f5b54ffd-763c-36ba.g",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/local-path-storage/local-path-storage-replicaset-local-path-provisioner-75f5b54ffd-763c-36ba.m",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/default/deployment-redis.g",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/default/deployment-redis.m",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/gadget/daemonset-gadget.g",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/gadget/daemonset-gadget.m",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-storage-debug-76f234.g",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-storage-debug-76f234.m",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-synchronizer-latest-63825b.g",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-synchronizer-latest-63825b.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/docker.io-qorbani-golang-hello-world-sha256-a14f3fbf3d5d1c4a000ab2c0c6d5e4633bdb96286a0130fa5b2c5967b934c31f-34c31f.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/docker.io-qorbani-golang-hello-world-sha256-a14f3fbf3d5d1c4a000ab2c0c6d5e4633bdb96286a0130fa5b2c5967b934c31f-34c31f.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-addon-resizer-1.8.18-gke.0-sha256-73f83a267713c9ec9bdb5564be404567b8d446813d39c74a5eff2fdbcc91ebf2-91ebf2.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-addon-resizer-1.8.18-gke.0-sha256-73f83a267713c9ec9bdb5564be404567b8d446813d39c74a5eff2fdbcc91ebf2-91ebf2.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-a146bc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-a146bc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-sha256-0f232ba18b63363e33f205d0242ef98324fb388434f8598c2fc8e967dca146bc-a146bc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-sha256-0f232ba18b63363e33f205d0242ef98324fb388434f8598c2fc8e967dca146bc-a146bc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-csi-node-driver-registrar-v2.8.0-gke.4-sha256-715a1581ce158fbf95f7ca351e25c7d6a0a1599e46e270e72238cc8a0aef1c43-ef1c43.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-csi-node-driver-registrar-v2.8.0-gke.4-sha256-715a1581ce158fbf95f7ca351e25c7d6a0a1599e46e270e72238cc8a0aef1c43-ef1c43.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-event-exporter-sha256-457dda454e42c2a7ccad69fe0af9cc3f005d734b24ad14f17ba88f74ba8b972e-8b972e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-event-exporter-sha256-457dda454e42c2a7ccad69fe0af9cc3f005d734b24ad14f17ba88f74ba8b972e-8b972e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-fluent-bit-gke-exporter-sha256-93014f5d546376de76c21f48bf30a6d1df3db4a413a1c3009c59fe46fa83eee8-83eee8.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-fluent-bit-gke-exporter-sha256-93014f5d546376de76c21f48bf30a6d1df3db4a413a1c3009c59fe46fa83eee8-83eee8.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-fluent-bit-sha256-c03635e4c828b9c6847df9780d6684b45ff0a70b1ae8c7e7271283cce472085e-72085e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-fluent-bit-sha256-c03635e4c828b9c6847df9780d6684b45ff0a70b1ae8c7e7271283cce472085e-72085e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-gcp-compute-persistent-disk-csi-driver-v1.10.7-gke.0-sha256-a3e4af6b6f6999427dc7b02e813aa1ca5f26e73357c92a77b8fe774ddf431a26-431a26.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-gcp-compute-persistent-disk-csi-driver-v1.10.7-gke.0-sha256-a3e4af6b6f6999427dc7b02e813aa1ca5f26e73357c92a77b8fe774ddf431a26-431a26.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-gke-metrics-agent-1.10.0-gke.0-sha256-0e56abb7da3b2419f6ef300a402c29f9e2810ba135db04621518581ffa48aae9-48aae9.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-gke-metrics-agent-1.10.0-gke.0-sha256-0e56abb7da3b2419f6ef300a402c29f9e2810ba135db04621518581ffa48aae9-48aae9.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-ingress-gce-404-server-with-metrics-v1.23.1-sha256-cf75158c683853c01e3af86209582cc2eaf102f5c0bc767ed0226e0fbdacde57-acde57.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-ingress-gce-404-server-with-metrics-v1.23.1-sha256-cf75158c683853c01e3af86209582cc2eaf102f5c0bc767ed0226e0fbdacde57-acde57.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-dnsmasq-nanny-1.22.22-gke.0-sha256-d7c0300eee5fb4998d3b60d92e5c07c9c4be2f489e04bdfa1950f2e23eb59bcc-b59bcc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-dnsmasq-nanny-1.22.22-gke.0-sha256-d7c0300eee5fb4998d3b60d92e5c07c9c4be2f489e04bdfa1950f2e23eb59bcc-b59bcc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-kube-dns-1.22.22-gke.0-sha256-76dcedf9b475902042f9ee22609e475fca96e29880315e9530a694bdd924897e-24897e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-kube-dns-1.22.22-gke.0-sha256-76dcedf9b475902042f9ee22609e475fca96e29880315e9530a694bdd924897e-24897e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-sidecar-1.22.22-gke.0-sha256-fd7dc24c8331bbd9d0178f65cfcfe7ef42c003b7ee25b8df595d80d0f237486a-37486a.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-k8s-dns-sidecar-1.22.22-gke.0-sha256-fd7dc24c8331bbd9d0178f65cfcfe7ef42c003b7ee25b8df595d80d0f237486a-37486a.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-metrics-server-v0.5.2-gke.3-sha256-1d20492ca374191e5b6ff4b7712b62b41ab75ce226424974356dc266e6e99e83-e99e83.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-metrics-server-v0.5.2-gke.3-sha256-1d20492ca374191e5b6ff4b7712b62b41ab75ce226424974356dc266e6e99e83-e99e83.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-alertmanager-v0.25.1-gmp.0-gke.1-sha256-927b106154a88f2c26fe68bd00fe96605564e4c654c71fa14b69b3d359fb8625-fb8625.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-alertmanager-v0.25.1-gmp.0-gke.1-sha256-927b106154a88f2c26fe68bd00fe96605564e4c654c71fa14b69b3d359fb8625-fb8625.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-config-reloader-v0.7.4-gke.0-sha256-7c290f7ac85228c341d79a05f1cbd75c309d6d0573c4ec32e113dc749e8076d9-8076d9.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-config-reloader-v0.7.4-gke.0-sha256-7c290f7ac85228c341d79a05f1cbd75c309d6d0573c4ec32e113dc749e8076d9-8076d9.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-operator-v0.7.4-gke.0-sha256-980b06655aca5de061fd422a6799ba9063861255851613ba612d668a86b92181-b92181.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-operator-v0.7.4-gke.0-sha256-980b06655aca5de061fd422a6799ba9063861255851613ba612d668a86b92181-b92181.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-prometheus-v2.41.0-gmp.4-gke.1-sha256-7d833aa877ee7e5fdc2df17005be8615af721ac3d01d7e257a3ae98d06516797-516797.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-prometheus-v2.41.0-gmp.4-gke.1-sha256-7d833aa877ee7e5fdc2df17005be8615af721ac3d01d7e257a3ae98d06516797-516797.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-rule-evaluator-v0.7.4-gke.0-sha256-6c7f0bb9d92ccdfa9a9f694c8f02ea200797c3c69d104a508e3faa62b70ad574-0ad574.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-engine-rule-evaluator-v0.7.4-gke.0-sha256-6c7f0bb9d92ccdfa9a9f694c8f02ea200797c3c69d104a508e3faa62b70ad574-0ad574.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-to-sd-sha256-8cd7e6b460418e25f80a4a0e8aa865bd5b716ea8750bfea4f6fc163c9b1c5dbb-1c5dbb.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-to-sd-sha256-8cd7e6b460418e25f80a4a0e8aa865bd5b716ea8750bfea4f6fc163c9b1c5dbb-1c5dbb.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-to-sd-v0.11.5-gke.0-sha256-654791db0d4d17c5847221fd3ace5c23ea1bb20c5976db9fda0853fd6000ab65-00ab65.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-prometheus-to-sd-v0.11.5-gke.0-sha256-654791db0d4d17c5847221fd3ace5c23ea1bb20c5976db9fda0853fd6000ab65-00ab65.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-proxy-agent-v0.1.3-gke.0-sha256-58325bb529432e3ea2ddfae7c35f9b86b2511d92ba5f8b1afa015ff904824f76-824f76.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/gke.gcr.io-proxy-agent-v0.1.3-gke.0-sha256-58325bb529432e3ea2ddfae7c35f9b86b2511d92ba5f8b1afa015ff904824f76-824f76.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-kubescape-v3.0.2-prerelease-66a0ac.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-kubescape-v3.0.2-prerelease-66a0ac.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-kubevuln-v0.2.133-bc6d6c.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-kubevuln-v0.2.133-bc6d6c.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-node-agent-v0.1.121-8d291a.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-node-agent-v0.1.121-8d291a.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-operator-v0.1.67-dc38da.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-kubescape-operator-v0.1.67-dc38da.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/docker.io-qorbani-golang-hello-world-sha256-a14f3fbf3d5d1c4a000ab2c0c6d5e4633bdb96286a0130fa5b2c5967b934c31f-34c31f.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/docker.io-qorbani-golang-hello-world-sha256-a14f3fbf3d5d1c4a000ab2c0c6d5e4633bdb96286a0130fa5b2c5967b934c31f-34c31f.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-addon-resizer-1.8.18-gke.0-sha256-73f83a267713c9ec9bdb5564be404567b8d446813d39c74a5eff2fdbcc91ebf2-91ebf2.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-addon-resizer-1.8.18-gke.0-sha256-73f83a267713c9ec9bdb5564be404567b8d446813d39c74a5eff2fdbcc91ebf2-91ebf2.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-a146bc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-a146bc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-sha256-0f232ba18b63363e33f205d0242ef98324fb388434f8598c2fc8e967dca146bc-a146bc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-cluster-proportional-autoscaler-1.8.4-gke.1-sha256-0f232ba18b63363e33f205d0242ef98324fb388434f8598c2fc8e967dca146bc-a146bc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-csi-node-driver-registrar-v2.8.0-gke.4-sha256-715a1581ce158fbf95f7ca351e25c7d6a0a1599e46e270e72238cc8a0aef1c43-ef1c43.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-csi-node-driver-registrar-v2.8.0-gke.4-sha256-715a1581ce158fbf95f7ca351e25c7d6a0a1599e46e270e72238cc8a0aef1c43-ef1c43.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-event-exporter-sha256-457dda454e42c2a7ccad69fe0af9cc3f005d734b24ad14f17ba88f74ba8b972e-8b972e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-event-exporter-sha256-457dda454e42c2a7ccad69fe0af9cc3f005d734b24ad14f17ba88f74ba8b972e-8b972e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-fluent-bit-gke-exporter-sha256-93014f5d546376de76c21f48bf30a6d1df3db4a413a1c3009c59fe46fa83eee8-83eee8.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-fluent-bit-gke-exporter-sha256-93014f5d546376de76c21f48bf30a6d1df3db4a413a1c3009c59fe46fa83eee8-83eee8.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-fluent-bit-sha256-c03635e4c828b9c6847df9780d6684b45ff0a70b1ae8c7e7271283cce472085e-72085e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-fluent-bit-sha256-c03635e4c828b9c6847df9780d6684b45ff0a70b1ae8c7e7271283cce472085e-72085e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-gcp-compute-persistent-disk-csi-driver-v1.10.7-gke.0-sha256-a3e4af6b6f6999427dc7b02e813aa1ca5f26e73357c92a77b8fe774ddf431a26-431a26.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-gcp-compute-persistent-disk-csi-driver-v1.10.7-gke.0-sha256-a3e4af6b6f6999427dc7b02e813aa1ca5f26e73357c92a77b8fe774ddf431a26-431a26.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-gke-metrics-agent-1.10.0-gke.0-sha256-0e56abb7da3b2419f6ef300a402c29f9e2810ba135db04621518581ffa48aae9-48aae9.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-gke-metrics-agent-1.10.0-gke.0-sha256-0e56abb7da3b2419f6ef300a402c29f9e2810ba135db04621518581ffa48aae9-48aae9.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-ingress-gce-404-server-with-metrics-v1.23.1-sha256-cf75158c683853c01e3af86209582cc2eaf102f5c0bc767ed0226e0fbdacde57-acde57.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-ingress-gce-404-server-with-metrics-v1.23.1-sha256-cf75158c683853c01e3af86209582cc2eaf102f5c0bc767ed0226e0fbdacde57-acde57.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-dnsmasq-nanny-1.22.22-gke.0-sha256-d7c0300eee5fb4998d3b60d92e5c07c9c4be2f489e04bdfa1950f2e23eb59bcc-b59bcc.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-dnsmasq-nanny-1.22.22-gke.0-sha256-d7c0300eee5fb4998d3b60d92e5c07c9c4be2f489e04bdfa1950f2e23eb59bcc-b59bcc.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-kube-dns-1.22.22-gke.0-sha256-76dcedf9b475902042f9ee22609e475fca96e29880315e9530a694bdd924897e-24897e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-kube-dns-1.22.22-gke.0-sha256-76dcedf9b475902042f9ee22609e475fca96e29880315e9530a694bdd924897e-24897e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-sidecar-1.22.22-gke.0-sha256-fd7dc24c8331bbd9d0178f65cfcfe7ef42c003b7ee25b8df595d80d0f237486a-37486a.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-k8s-dns-sidecar-1.22.22-gke.0-sha256-fd7dc24c8331bbd9d0178f65cfcfe7ef42c003b7ee25b8df595d80d0f237486a-37486a.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-metrics-server-v0.5.2-gke.3-sha256-1d20492ca374191e5b6ff4b7712b62b41ab75ce226424974356dc266e6e99e83-e99e83.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-metrics-server-v0.5.2-gke.3-sha256-1d20492ca374191e5b6ff4b7712b62b41ab75ce226424974356dc266e6e99e83-e99e83.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-alertmanager-v0.25.1-gmp.0-gke.1-sha256-927b106154a88f2c26fe68bd00fe96605564e4c654c71fa14b69b3d359fb8625-fb8625.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-alertmanager-v0.25.1-gmp.0-gke.1-sha256-927b106154a88f2c26fe68bd00fe96605564e4c654c71fa14b69b3d359fb8625-fb8625.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-config-reloader-v0.7.4-gke.0-sha256-7c290f7ac85228c341d79a05f1cbd75c309d6d0573c4ec32e113dc749e8076d9-8076d9.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-config-reloader-v0.7.4-gke.0-sha256-7c290f7ac85228c341d79a05f1cbd75c309d6d0573c4ec32e113dc749e8076d9-8076d9.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-operator-v0.7.4-gke.0-sha256-980b06655aca5de061fd422a6799ba9063861255851613ba612d668a86b92181-b92181.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-operator-v0.7.4-gke.0-sha256-980b06655aca5de061fd422a6799ba9063861255851613ba612d668a86b92181-b92181.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-prometheus-v2.41.0-gmp.4-gke.1-sha256-7d833aa877ee7e5fdc2df17005be8615af721ac3d01d7e257a3ae98d06516797-516797.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-prometheus-v2.41.0-gmp.4-gke.1-sha256-7d833aa877ee7e5fdc2df17005be8615af721ac3d01d7e257a3ae98d06516797-516797.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-rule-evaluator-v0.7.4-gke.0-sha256-6c7f0bb9d92ccdfa9a9f694c8f02ea200797c3c69d104a508e3faa62b70ad574-0ad574.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-engine-rule-evaluator-v0.7.4-gke.0-sha256-6c7f0bb9d92ccdfa9a9f694c8f02ea200797c3c69d104a508e3faa62b70ad574-0ad574.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-to-sd-sha256-8cd7e6b460418e25f80a4a0e8aa865bd5b716ea8750bfea4f6fc163c9b1c5dbb-1c5dbb.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-to-sd-sha256-8cd7e6b460418e25f80a4a0e8aa865bd5b716ea8750bfea4f6fc163c9b1c5dbb-1c5dbb.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-to-sd-v0.11.5-gke.0-sha256-654791db0d4d17c5847221fd3ace5c23ea1bb20c5976db9fda0853fd6000ab65-00ab65.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-prometheus-to-sd-v0.11.5-gke.0-sha256-654791db0d4d17c5847221fd3ace5c23ea1bb20c5976db9fda0853fd6000ab65-00ab65.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-proxy-agent-v0.1.3-gke.0-sha256-58325bb529432e3ea2ddfae7c35f9b86b2511d92ba5f8b1afa015ff904824f76-824f76.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/gke.gcr.io-proxy-agent-v0.1.3-gke.0-sha256-58325bb529432e3ea2ddfae7c35f9b86b2511d92ba5f8b1afa015ff904824f76-824f76.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-kubescape-v3.0.2-prerelease-66a0ac.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-kubescape-v3.0.2-prerelease-66a0ac.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-kubevuln-v0.2.133-bc6d6c.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-kubevuln-v0.2.133-bc6d6c.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-node-agent-v0.1.121-8d291a.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-node-agent-v0.1.121-8d291a.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-operator-v0.1.67-dc38da.g",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-kubescape-operator-v0.1.67-dc38da.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.g",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.g",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/gmp-system/statefulset-alertmanager-config-reloader.g",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/gmp-system/statefulset-alertmanager-config-reloader.m",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscans/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.g",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscans/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.m",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.g",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.m",
	}

	filesDeleted := handler.GetFilesToDelete()
	slices.Sort(filesDeleted)

	assert.Equal(t, expectedFilesToDelete, filesDeleted)
}

type ResourcesFetchMock struct {
}

var _ ResourcesFetcher = (*ResourcesFetchMock)(nil)

func (r *ResourcesFetchMock) FetchResources() (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           sets.NewSet[string](),
		RunningContainerImageIds:     sets.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, sets.Set[string]]),
	}

	var expectedImageIds []string
	if err := json.Unmarshal(imageIds, &expectedImageIds); err != nil {
		panic(err)
	}
	resourceMaps.RunningContainerImageIds.Append(expectedImageIds...)

	var expectedInstanceIds []string
	if err := json.Unmarshal(instanceIds, &expectedInstanceIds); err != nil {
		panic(err)
	}
	resourceMaps.RunningInstanceIds.Append(expectedInstanceIds...)

	var expectedWlids map[string][]string
	if err := json.Unmarshal(wlids, &expectedWlids); err != nil {
		panic(err)
	}
	for wlid, containerNames := range expectedWlids {
		resourceMaps.RunningWlidsToContainerNames.Set(wlid, sets.NewSet(containerNames...))
	}

	return resourceMaps, nil
}

func unzipSource(source string, appFs afero.Fs) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		err := unzipFile(f, file.DefaultStorageRoot, appFs)
		if err != nil {
			return err
		}
	}

	return nil
}

func unzipFile(f *zip.File, destination string, appFs afero.Fs) error {
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", filePath)
	}

	if f.FileInfo().IsDir() {
		if err := appFs.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}

	if err := appFs.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	destinationFile, err := appFs.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	zippedFile, err := f.Open()
	if err != nil {
		return err
	}
	defer zippedFile.Close()

	if _, err := io.Copy(destinationFile, zippedFile); err != nil {
		return err
	}
	return nil
}
