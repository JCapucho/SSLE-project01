package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/server/v3/etcdserver"
	"google.golang.org/grpc"

	"ssle/registry/state"
	"ssle/registry/utils"
	pb "ssle/services"
)

type ObserverAPIServer struct {
	pb.UnimplementedObserverAPIServer
	State      *state.State
	EtcdServer *etcdserver.EtcdServer
}

func (server *ObserverAPIServer) GetDatacenterServices(ctx context.Context, req *pb.GetDatacenterServicesRequest) (*pb.GetDatacenterServicesResponse, error) {
	node, err := utils.AuthenticateObserver(ctx, server.EtcdServer)
	if err != nil {
		return nil, err
	}

	prefix := fmt.Appendf(nil, "%s/%s/", utils.DCServicesNamespace, node.Datacenter)

	res, err := server.EtcdServer.Range(ctx, &etcdserverpb.RangeRequest{
		Key:      prefix,
		RangeEnd: utils.PrefixEnd(prefix),
	})
	if err != nil {
		log.Printf("Error fetching datacenter services: %v", err)
		return nil, utils.ServerError
	}

	svcs := make([]*pb.ServiceSpec, len(res.Kvs))
	for i, kv := range res.Kvs {
		err = json.Unmarshal(kv.Value, &svcs[i])
		if err != nil {
			log.Printf("Error decoding datacenter service: %v", err)
			return nil, utils.ServerError
		}
	}

	return &pb.GetDatacenterServicesResponse{Services: svcs}, nil
}

func (server *ObserverAPIServer) WatchDatacenterServices(req *pb.WatchDatacenterServicesRequest, stream grpc.ServerStreamingServer[pb.WatchDatacenterServicesResponse]) error {
	node, err := utils.AuthenticateObserver(stream.Context(), server.EtcdServer)
	if err != nil {
		return err
	}

	prefix := fmt.Appendf(nil, "%s/%s/", utils.DCServicesNamespace, node.Datacenter)

	kv := server.EtcdServer.Watchable()
	watchStream := kv.NewWatchStream()
	defer watchStream.Close()

	watchStream.Watch(0, prefix, utils.PrefixEnd(prefix), 0)

	for {
		select {
		case msg := <-watchStream.Chan():
			for _, event := range msg.Events {
				var msg pb.WatchDatacenterServicesResponse
				switch event.Type {
				case mvccpb.PUT:
					var svc pb.ServiceSpec
					err = json.Unmarshal(event.Kv.Value, &svc)
					if err != nil {
						log.Printf("Error decoding datacenter service: %v", err)
						return utils.ServerError
					}

					msg = pb.WatchDatacenterServicesResponse{
						Notification: &pb.WatchDatacenterServicesResponse_Update{
							Update: &pb.WatchServiceUpdate{
								Service: &svc,
							},
						},
					}
				case mvccpb.DELETE:
					parts := bytes.Split(event.Kv.Key, []byte("/"))

					if len(parts) < 3 {
						log.Printf("Malformed datacenter service key: %s", event.Kv.Key)
						return utils.ServerError
					}

					node := string(parts[len(parts)-3])
					service_name := string(parts[len(parts)-2])
					instance := string(parts[len(parts)-1])

					msg = pb.WatchDatacenterServicesResponse{
						Notification: &pb.WatchDatacenterServicesResponse_Delete{
							Delete: &pb.WatchServiceDelete{
								Node:        &node,
								ServiceName: &service_name,
								Instance:    &instance,
							},
						},
					}
				}

				if err := stream.Send(&msg); err != nil {
					log.Printf("Error streaming service changes: %v", err)
					return utils.ServerError
				}
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}
