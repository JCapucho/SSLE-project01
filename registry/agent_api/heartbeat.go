package agent_api

import (
	"context"

	"ssle/registry/utils"
	pb "ssle/services"
)

func (server *AgentAPIServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	node, err := utils.AuthenticateAgent(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	// Retrieve the node lease to renew it
	utils.GetNodeLease(ctx, server.EtcdServer, node.Datacenter.String(), node.Name.String())

	return &pb.HeartbeatResponse{}, nil
}
