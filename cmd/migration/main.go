package main

// migration is a standalone utility to decode legacy Gob data into JSON.
// It is used to handle breaking changes in the Gob format (e.g., transitioning 
// fields from uint64 to int64) that cannot be handled by the main service 
// due to Go's global Gob type registration constraints.

import (
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Legacy types with uint64 fields exactly as they were in the old binary format
type LegacyArg struct {
	Index    uint64 `json:"index"`
	Value    uint64 `json:"value"`
	ValueTwo uint64 `json:"valueTwo"`
	Op       string `json:"op"`
}

type LegacySyscall struct {
	Names    []string     `json:"names"`
	Action   string       `json:"action"`
	ErrnoRet uint64       `json:"errnoRet"`
	Args     []*LegacyArg `json:"args"`
}

type LegacyExecCalls struct {
	Path string   `json:"Path"`
	Args []string `json:"Args"`
	Envs []string `json:"Envs"`
}

type LegacyOpenCalls struct {
	Path  string   `json:"Path"`
	Flags []string `json:"Flags"`
}

type LegacyApplicationProfileContainer struct {
	Name                 string                                    `json:"Name"`
	Capabilities         []string                                  `json:"Capabilities"`
	Execs                []LegacyExecCalls                         `json:"Execs"`
	Opens                []LegacyOpenCalls                         `json:"Opens"`
	Syscalls             []string                                  `json:"Syscalls"`
	SeccompProfile       LegacySingleSeccompProfile                `json:"SeccompProfile"`
	Endpoints            []softwarecomposition.HTTPEndpoint        `json:"Endpoints"`
	ImageID              string                                    `json:"ImageID"`
	ImageTag             string                                    `json:"ImageTag"`
	PolicyByRuleId       map[string]softwarecomposition.RulePolicy `json:"PolicyByRuleId"`
	IdentifiedCallStacks []softwarecomposition.IdentifiedCallStack  `json:"IdentifiedCallStacks"`
}

type LegacyApplicationProfileSpec struct {
	Architectures       []string                            `json:"Architectures,omitempty"`
	Containers          []LegacyApplicationProfileContainer `json:"Containers,omitempty"`
	InitContainers      []LegacyApplicationProfileContainer `json:"InitContainers,omitempty"`
	EphemeralContainers []LegacyApplicationProfileContainer `json:"EphemeralContainers,omitempty"`
}

type LegacyApplicationProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:",inline"`

	// +k8s:conversion-gen=false
	Parts map[string]string `json:"Parts,omitempty"`
	// +k8s:conversion-gen=false
	SchemaVersion int64                        `json:"SchemaVersion,omitempty"`
	Spec          LegacyApplicationProfileSpec `json:"Spec,omitempty"`
}

type LegacySingleSeccompProfile struct {
	Name string `json:"Name"`
	Path string `json:"Path"`
	Spec struct {
		softwarecomposition.SpecBase `json:",inline"`
		BaseProfileName              string           `json:"BaseProfileName"`
		DefaultAction                string           `json:"DefaultAction"`
		Architectures                []string         `json:"Architectures"`
		ListenerPath                 string           `json:"ListenerPath"`
		ListenerMetadata             string           `json:"ListenerMetadata"`
		Syscalls                     []*LegacySyscall `json:"Syscalls"`
		Flags                        []string         `json:"Flags"`
	} `json:"Spec"`
}

type LegacyContainerProfileSpec struct {
	Architectures        []string                                  `json:"Architectures"`
	Capabilities         []string                                  `json:"Capabilities"`
	Execs                []LegacyExecCalls                         `json:"Execs"`
	Opens                []LegacyOpenCalls                         `json:"Opens"`
	Syscalls             []string                                  `json:"Syscalls"`
	SeccompProfile       LegacySingleSeccompProfile                `json:"SeccompProfile"`
	Endpoints            []softwarecomposition.HTTPEndpoint        `json:"Endpoints"`
	ImageID              string                                    `json:"ImageID"`
	ImageTag             string                                    `json:"ImageTag"`
	PolicyByRuleId       map[string]softwarecomposition.RulePolicy `json:"PolicyByRuleId"`
	IdentifiedCallStacks []softwarecomposition.IdentifiedCallStack  `json:"IdentifiedCallStacks"`
	metav1.LabelSelector `json:"LabelSelector"`
	Ingress              []LegacyNetworkNeighbor                   `json:"Ingress"`
	Egress               []LegacyNetworkNeighbor                   `json:"Egress"`
}

type LegacyContainerProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:",inline"`
	Spec              LegacyContainerProfileSpec `json:"Spec,omitempty"`
	Status            softwarecomposition.ContainerProfileStatus `json:"Status,omitempty"`
}

type LegacyNetworkPort struct {
	Name     string `json:"Name"`
	Protocol string `json:"Protocol"`
	Port     *int32  `json:"Port"`
}

type LegacyNetworkNeighbor struct {
	Identifier        string                      `json:"Identifier"`
	Type              string                      `json:"Type"`
	DNS               string                      `json:"DNS"`
	DNSNames          []string                    `json:"DNSNames"`
	Ports             []LegacyNetworkPort         `json:"Ports"`
	PodSelector       *metav1.LabelSelector       `json:"PodSelector"`
	NamespaceSelector *metav1.LabelSelector       `json:"NamespaceSelector"`
	IPAddress         string                      `json:"IPAddress"`
}

type LegacySeccompProfileSpec struct {
	Containers          []LegacySingleSeccompProfile `json:"Containers,omitempty"`
	InitContainers      []LegacySingleSeccompProfile `json:"InitContainers,omitempty"`
	EphemeralContainers []LegacySingleSeccompProfile `json:"EphemeralContainers,omitempty"`
}

type LegacySeccompProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:",inline"`
	Spec              LegacySeccompProfileSpec            `json:"Spec,omitempty"`
	Status            softwarecomposition.SeccompProfileStatus `json:"Status,omitempty"`
}

func main() {
	filePath := flag.String("file", "", "Path to the gob file to decode")
	typeName := flag.String("type", "ApplicationProfile", "Type to decode (ApplicationProfile, ContainerProfile, or SeccompProfile)")
	flag.Parse()

	if *filePath == "" {
		fmt.Fprintf(os.Stderr, "Usage: migration -file <path> [-type <ApplicationProfile|ContainerProfile|SeccompProfile>]\n")
		os.Exit(1)
	}

	f, err := os.Open(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var result interface{}
	switch *typeName {
	case "ApplicationProfile":
		result = &LegacyApplicationProfile{}
	case "ContainerProfile":
		result = &LegacyContainerProfile{}
	case "SeccompProfile":
		result = &LegacySeccompProfile{}
	default:
		fmt.Fprintf(os.Stderr, "unsupported type: %s\n", *typeName)
		os.Exit(1)
	}

	// Important: We need to register types that might be in the gob stream
	// but are defined locally in this 'main' package to avoid name mismatches
	// although gob name matching is usually package-scoped.
	// Since this is a separate binary, its 'main.LegacyApplicationProfile'
	// registration is isolated from the storage binary's registration.

	// Register common types that might be inside interface{} fields or nested structs
	gob.Register(map[string]interface{}{})
	gob.Register([]interface{}{})
	gob.Register(metav1.Time{})

	decoder := gob.NewDecoder(f)
	if err := decoder.Decode(result); err != nil {
		fmt.Fprintf(os.Stderr, "decode failed: %v\n", err)
		os.Exit(1)
	}

	out, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s", out)
}
