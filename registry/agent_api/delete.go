package agent_api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/server/v3/lease"

	"ssle/registry/utils"
	pb "ssle/services"
)

func (server *AgentAPIServer) Deregister(ctx context.Context, req *pb.DeregisterServiceRequest) (*pb.DeregisterServiceResponse, error) {
	node, err := utils.AuthenticateAgent(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	svcKey := fmt.Appendf(
		nil,
		"%s/%s/%s/%s/%s/%s",
		utils.ServiceNamespace,
		*req.Service,
		node.Location,
		node.Datacenter,
		node.Name,
		*req.Instance,
	)
	dsSvcKey := fmt.Appendf(
		nil,
		"%s/%s/%s/%s/%s",
		utils.DCServicesNamespace,
		node.Datacenter,
		node.Name,
		*req.Service,
		*req.Instance,
	)

	txn := &etcdserverpb.TxnRequest{
		Success: []*etcdserverpb.RequestOp{
			{
				Request: &etcdserverpb.RequestOp_RequestDeleteRange{
					RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
						Key: svcKey,
					},
				},
			},
			{
				Request: &etcdserverpb.RequestOp_RequestDeleteRange{
					RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
						Key: dsSvcKey,
					},
				},
			},
		},
	}

	res, err := server.EtcdServer.Txn(ctx, txn)
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	if !res.Succeeded {
		log.Printf("Error: Failed to delete service: %v", res)
		return nil, utils.ServerError
	}

	return &pb.DeregisterServiceResponse{}, nil
}

func (server *AgentAPIServer) Reset(ctx context.Context, req *pb.ResetRequest) (*pb.ResetResponse, error) {
	node, err := utils.AuthenticateAgent(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	key := fmt.Appendf(nil, "%s/%s/%s", utils.NodesLeasesNamespace, node.Datacenter, node.Name)

	// Get lease number
	res, err := server.EtcdServer.Range(ctx, &etcdserverpb.RangeRequest{Key: key})
	if err != nil {
		return nil, utils.ServerError
	}

	if len(res.Kvs) != 0 {
		var leaseId int64
		err := json.Unmarshal(res.Kvs[0].Value, &leaseId)
		// If we have an error in decoding the lease id, something must have
		// gone wrong when storing it, so ignore it, worst case the lease
		// will expire.
		if err == nil {
			_, err := server.EtcdServer.LeaseRevoke(ctx, &etcdserverpb.LeaseRevokeRequest{ID: leaseId})
			if err != nil && !errors.Is(err, lease.ErrLeaseNotFound) {
				return nil, utils.ServerError
			}
		}
	}

	return &pb.ResetResponse{}, nil
}
