package state

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	dockerClient "github.com/docker/docker/client"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"ssle/agent/config"
	"ssle/node-utils"
	"ssle/services"
)

const (
	registryResolverScheme = "registry"
)

type State struct {
	*node_utils.NodeState

	AgentClient  services.AgentAPIClient
	DockerClient *dockerClient.Client

	SignatureVerifier *verify.Verifier

	eventsFile *os.File
}

func LoadState(config *config.Config) *State {
	nodeState := node_utils.LoadNodeState(
		config.Dir,
		config.CAFile,
		config.CrtFile,
		config.KeyFile,
		strings.Split(config.JoinUrl, ","),
	)

	opts := tuf.DefaultOptions()
	client, err := tuf.New(opts)
	if err != nil {
		panic(err)
	}

	trustedMaterial, err := root.GetTrustedRoot(client)
	if err != nil {
		panic(err)
	}

	verifier, err := verify.NewVerifier(
		trustedMaterial,
		verify.WithTransparencyLog(1),
		verify.WithIntegratedTimestamps(1),
	)
	if err != nil {
		panic(err)
	}

	eventsFile, err := os.OpenFile(config.EventsLog, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatalf("Failed to open events log: %v", err)
	}

	dcli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}

	return &State{
		NodeState:         nodeState,
		AgentClient:       services.NewAgentAPIClient(nodeState.Connection),
		DockerClient:      dcli,
		SignatureVerifier: verifier,
		eventsFile:        eventsFile,
	}
}

func (state *State) WriteEvent(event any) {
	msg, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to encode event: %v", err)
		return
	}
	msg = fmt.Appendf(msg, "\n")
	state.eventsFile.Write(msg)
}
