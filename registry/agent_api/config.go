package agent_api

import (
	"context"
	"log"
	"time"

	"ssle/registry/utils"
	pb "ssle/services"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

func (server *AgentAPIServer) Config(ctx context.Context, req *pb.ConfigRequest) (*pb.ConfigResponse, error) {
	cert, err := utils.ExtractPeerCertificate(ctx)
	if err != nil {
		return nil, err
	}

	// Extract
	node, err := utils.AuthenticateAgentFromCertificate(ctx, cert, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	// Time between renews
	renewPeriod := utils.AgentCertificateExpiry / 2
	// Calculate the time until a renew
	now := time.Now()
	renewAt := cert.NotAfter.Sub(now) - renewPeriod

	heartbeatPeriod := utils.NodeKeepaliveTTL / 2

	res := &pb.ConfigResponse{
		HeartbeatPeriod: &heartbeatPeriod,
	}

	// If the certificate should already be renewed, do it
	if renewAt < 0 {
		log.Printf("Renewing certificate for %v/%v", node.Datacenter, node.Name)

		cert, key := utils.CreateAgentCrt(server.State, node.Datacenter, node.Name)

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
