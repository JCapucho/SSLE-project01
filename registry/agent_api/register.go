package agent_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"google.golang.org/grpc/peer"

	"ssle/registry/utils"
	pb "ssle/services"
)

func (server *AgentAPIServer) Register(ctx context.Context, req *pb.RegisterServiceRequest) (*pb.RegisterServiceResponse, error) {
	node, err := utils.AuthenticateAgent(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, utils.AuthFailure
	}

	spec := pb.ServiceSpec{
		ServiceName: req.Service,
		Instance:    req.Instance,

		Location:   (*string)(&node.Location),
		Datacenter: (*string)(&node.Datacenter),
		Node:       (*string)(&node.Name),

		Addresses:   req.Addresses,
		Ports:       req.Ports,
		MetricsPort: req.MetricsPort,
	}

	if len(spec.Addresses) == 0 {
		ip, _, err := net.SplitHostPort(p.Addr.String())
		if err != nil {
			log.Print(err.Error())
			return nil, utils.ServerError
		}

		spec.Addresses = []string{ip}
	}

	svcKey := fmt.Appendf(
		nil,
		"%s/%s/%s/%s/%s/%s",
		utils.ServiceNamespace,
		*spec.ServiceName,
		node.Location,
		node.Datacenter,
		node.Name,
		*spec.Instance,
	)
	dsSvcKey := fmt.Appendf(
		nil,
		"%s/%s/%s/%s/%s",
		utils.DCServicesNamespace,
		node.Datacenter,
		node.Name,
		*spec.ServiceName,
		*spec.Instance,
	)

	serializedSpec, err := json.Marshal(&spec)
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	nodeLease, err := utils.GetNodeLease(ctx, server.EtcdServer, node.Datacenter, node.Name)
	if err != nil {
		return nil, err
	}

	res, err := server.EtcdServer.Txn(ctx, &etcdserverpb.TxnRequest{
		Success: []*etcdserverpb.RequestOp{
			{
				Request: &etcdserverpb.RequestOp_RequestPut{
					RequestPut: &etcdserverpb.PutRequest{
						Key:   svcKey,
						Value: serializedSpec,
						Lease: nodeLease,
					},
				},
			},
			{
				Request: &etcdserverpb.RequestOp_RequestPut{
					RequestPut: &etcdserverpb.PutRequest{
						Key:   dsSvcKey,
						Value: serializedSpec,
						Lease: nodeLease,
					},
				},
			},
		},
	})
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	if !res.Succeeded {
		log.Printf("Error: Failed to register service: %v", res)
		return nil, utils.ServerError
	}

	return &pb.RegisterServiceResponse{
		Service: &spec,
	}, nil
}
