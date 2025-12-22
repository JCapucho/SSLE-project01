package agent_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"

	"go.etcd.io/etcd/api/v3/etcdserverpb"

	"ssle/registry/utils"
	pb "ssle/services"
)

const (
	MaxGetServiceLimit = 3
)

func (server *AgentAPIServer) getServiceInternal(
	ctx context.Context,
	prefix []byte,
	limit int,
) (map[string]*pb.ServiceSpec, error) {
	res, err := server.EtcdServer.Range(ctx, &etcdserverpb.RangeRequest{
		Key:      prefix,
		RangeEnd: utils.PrefixEnd(prefix),
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, err
	}

	svcs := make(map[string]*pb.ServiceSpec, len(res.Kvs))
	for _, kv := range res.Kvs {
		var tmp pb.ServiceSpec
		err = json.Unmarshal(kv.Value, &tmp)
		if err != nil {
			return nil, err
		}
		svcs[string(kv.Key)] = &tmp
	}

	return svcs, nil
}

func (server *AgentAPIServer) fillServices(
	ctx context.Context,
	prefix []byte,
	svcs map[string]*pb.ServiceSpec,
) (map[string]*pb.ServiceSpec, error) {
	extra, err := server.getServiceInternal(ctx, prefix, MaxGetServiceLimit)
	if err != nil {
		return svcs, nil
	}

	for k, v := range extra {
		if len(svcs) >= MaxGetServiceLimit {
			break
		}
		svcs[k] = v
	}

	return svcs, nil
}

func (server *AgentAPIServer) Discover(ctx context.Context, req *pb.DiscoverRequest) (*pb.DiscoverResponse, error) {
	node, err := utils.AuthenticateAgent(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	svc := *req.Service
	name := node.Name.String()
	dc := node.Datacenter.String()
	location := node.Location.String()

	if req.Location != nil {
		location = *req.Location
	}
	if req.Datacenter != nil {
		dc = *req.Datacenter
	}
	if req.Node != nil {
		name = *req.Node
	}

	svcPrefix := fmt.Appendf(nil, "%v/%v/", utils.ServiceNamespace, svc)
	locPrefix := fmt.Appendf(svcPrefix, "%v/", location)
	dcPrefix := fmt.Appendf(locPrefix, "%v/", dc)
	namePrefix := fmt.Appendf(dcPrefix, "%v/", name)

	var svcs map[string]*pb.ServiceSpec

	if req.Instance != nil {
		key := fmt.Appendf(namePrefix, "%v", req.Instance)
		svcs, err = server.getServiceInternal(ctx, key, 1)
	} else {
		svcs, err = server.getServiceInternal(ctx, namePrefix, MaxGetServiceLimit)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && req.Node == nil {
		log.Print("Querying datacenter services")
		svcs, err = server.fillServices(ctx, dcPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && req.Datacenter == nil {
		log.Print("Querying location services")
		svcs, err = server.fillServices(ctx, locPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && req.Location == nil {
		log.Print("Querying global services")
		svcs, err = server.fillServices(ctx, svcPrefix, svcs)
	}

	if err != nil {
		return nil, err
	}

	return &pb.DiscoverResponse{Services: slices.Collect(maps.Values(svcs))}, nil
}
