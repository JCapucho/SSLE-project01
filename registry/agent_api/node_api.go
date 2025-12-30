package agent_api

import (
	"context"
	"log"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/server/v3/etcdserver"

	"ssle/registry/state"
	"ssle/registry/utils"
	"ssle/services"
)

type NodeAPIServer struct {
	services.UnimplementedNodeAPIServer
	State      *state.State
	EtcdServer *etcdserver.EtcdServer
}

func (server *NodeAPIServer) Config(ctx context.Context, req *services.ConfigRequest) (*services.ConfigResponse, error) {
	cert, err := utils.ExtractPeerCertificate(ctx)
	if err != nil {
		return nil, err
	}

	// Extract
	role, node, err := utils.AuthenticateNodeFromCertificate(ctx, cert, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	// Time between renews
	renewPeriod := utils.NodeCertificateExpiry / 2
	// Calculate the time until a renew
	now := time.Now()
	renewAt := cert.NotAfter.Sub(now) - renewPeriod

	heartbeatPeriod := utils.NodeKeepaliveTTL / 2

	res := &services.ConfigResponse{
		HeartbeatPeriod: &heartbeatPeriod,
	}

	// If the certificate should already be renewed, do it
	if renewAt < 0 {
		log.Printf("Renewing certificate for %v/%v", node.Datacenter, node.Name)

		cert, key := utils.CreateNodeCrt(server.State, node.Datacenter, node.Name, role)

		res.Certificate = cert
		res.Key = key
		renewPeriod := uint64(renewPeriod.Seconds())
		res.RenewPeriod = &renewPeriod
	} else {
		renewAt := uint64(renewAt.Seconds())
		res.RenewPeriod = &renewAt
	}

	addrsRes, err := server.EtcdServer.Range(ctx, &etcdserverpb.RangeRequest{
		Key:      []byte(utils.PeerAgentApiNamespace),
		RangeEnd: utils.PrefixEnd([]byte(utils.PeerAgentApiNamespace)),
	})
	if err != nil {
		return nil, utils.ServerError
	}

	addrs := []string{}
	for _, kv := range addrsRes.Kvs {
		addrs = append(addrs, string(kv.Value))
	}
	res.RegistryAddrs = addrs

	return res, nil
}

func (server *NodeAPIServer) Heartbeat(ctx context.Context, req *services.HeartbeatRequest) (*services.HeartbeatResponse, error) {
	cert, err := utils.ExtractPeerCertificate(ctx)
	if err != nil {
		return nil, err
	}

	// Extract
	_, node, err := utils.AuthenticateNodeFromCertificate(ctx, cert, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	// Retrieve the node lease to renew it
	utils.GetNodeLease(ctx, server.EtcdServer, node.Datacenter, node.Name)

	return &services.HeartbeatResponse{}, nil
}
