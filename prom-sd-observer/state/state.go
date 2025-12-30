package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"
	"sync"

	"ssle/node-utils"
	"ssle/prom-sd-observer/config"
	"ssle/services"
)

const (
	registryResolverScheme = "registry"
)

type ServiceKey struct {
	Node        string
	ServiceName string
	Instance    string
}

type PrometheusService struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

type State struct {
	*node_utils.NodeState

	ObserverClient services.ObserverAPIClient

	targets       map[ServiceKey]PrometheusService
	targetsFile   *os.File
	targetsFileMU sync.Mutex
}

func LoadState(config *config.Config) *State {
	nodeState := node_utils.LoadNodeState(
		config.Dir,
		config.CAFile,
		config.CrtFile,
		config.KeyFile,
		strings.Split(config.JoinUrl, ","),
	)

	targetsFile, err := os.Create(config.TargetsFile)
	if err != nil {
		log.Fatalf("Failed to open events log: %v", err)
	}

	state := &State{
		NodeState:      nodeState,
		ObserverClient: services.NewObserverAPIClient(nodeState.Connection),
		targets:        map[ServiceKey]PrometheusService{},
		targetsFile:    targetsFile,
	}

	state.writeServices()

	return state
}

func (state *State) writeServices() {
	buf := bytes.NewBuffer([]byte{})
	encoder := json.NewEncoder(buf)

	first := true
	buf.WriteString("[")
	for _, v := range state.targets {
		if !first {
			buf.WriteString(",")
		} else {
			first = false
		}

		encoder.Encode(v)
		buf.Truncate(buf.Len() - 1)
	}
	buf.WriteString("]")

	err := state.targetsFile.Truncate(0)
	if err != nil {
		log.Fatal(err.Error())
	}

	_, err = state.targetsFile.WriteAt(buf.Bytes(), 0)
}

func (state *State) updateService(svc *services.ServiceSpec) {
	if svc.MetricsPort == nil || *svc.MetricsPort == 0 {
		log.Printf("Skipping %s/%s, no metrics port", *svc.Node, *svc.Instance)
		return
	}

	key := ServiceKey{
		Node:        *svc.Node,
		ServiceName: *svc.ServiceName,
		Instance:    *svc.Instance,
	}

	targets := make([]string, len(svc.Addresses))
	for i, addr := range svc.Addresses {
		ip, err := netip.ParseAddr(addr)

		if err == nil {
			if ip.Is4() {
				targets[i] = fmt.Sprintf("%s:%d", ip.String(), *svc.MetricsPort)
			} else {
				targets[i] = fmt.Sprintf("[%s]:%d", ip.String(), *svc.MetricsPort)
			}
		} else {
			targets[i] = fmt.Sprintf("%s:%d", addr, *svc.MetricsPort)
		}
	}

	promSvc := PrometheusService{
		Targets: targets,
		Labels: map[string]string{
			"__meta_location":       *svc.Location,
			"__meta_node":           *svc.Location,
			"__meta_datacenter":     *svc.Location,
			"__meta_prometheus_job": *svc.Location,
			"__meta_instance":       *svc.Instance,
		},
	}

	state.targets[key] = promSvc
}

func (state *State) UpdateService(svc *services.ServiceSpec) {
	state.targetsFileMU.Lock()
	defer state.targetsFileMU.Unlock()

	state.updateService(svc)

	state.writeServices()
}

func (state *State) UpdateServiceBulk(svcs []*services.ServiceSpec) {
	state.targetsFileMU.Lock()
	defer state.targetsFileMU.Unlock()

	for _, svc := range svcs {
		state.updateService(svc)
	}

	state.writeServices()
}

func (state *State) DeleteService(node string, serviceName string, instance string) {
	state.targetsFileMU.Lock()
	defer state.targetsFileMU.Unlock()

	delete(state.targets, ServiceKey{
		Node:        node,
		ServiceName: serviceName,
		Instance:    instance,
	})

	state.writeServices()
}
