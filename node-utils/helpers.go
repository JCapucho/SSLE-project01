package node_utils

import (
	"context"
	"log"
	"time"

	"ssle/services"
)

func (state *NodeState) GetRegistryConfig() (*services.ConfigResponse, error) {
	res, err := state.NodeApi.Config(context.Background(), &services.ConfigRequest{})
	if err != nil {
		return nil, err
	}

	if res.Certificate != nil && res.Key != nil {
		err = state.UpdateCredentials(res.Certificate, res.Key)
		if err != nil {
			log.Printf("Failed to update agent credentials: %v", err)
		}
	}

	state.UpdateAddrs(res.RegistryAddrs)

	return res, nil
}

func (state *NodeState) ConfigBackgroundJob() {
	for {
		_, err := state.GetRegistryConfig()
		if err != nil {
			log.Printf("Failed to refresh agent config: %v", err)
			continue
		}

		// Refresh config every minute
		time.Sleep(time.Minute)
	}
}

func (state *NodeState) HeartbeatBackgroundJob(period time.Duration) {
	for {
		_, err := state.NodeApi.Heartbeat(context.Background(), &services.HeartbeatRequest{})
		if err != nil {
			log.Printf("Failed to send heartbeat: %v", err)
			continue
		}

		// Refresh config every minute
		time.Sleep(period * time.Second)
	}
}
