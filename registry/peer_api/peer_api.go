package peer_api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"ssle/registry/config"
	"ssle/registry/schemas"
	"ssle/registry/state"
	"ssle/registry/utils"
	pb "ssle/services"
)

var (
	InvalidAdvertiseUrlError = status.Errorf(codes.InvalidArgument, "Advertise url is not a valid url")
	MissingAdvertiseUrlError = status.Errorf(codes.InvalidArgument, "At least one advertised URL must be set")

	AgentAlreadyExistsError = status.Errorf(codes.AlreadyExists, "Agent already exists")
)

type PeerAPIServer struct {
	pb.UnimplementedPeerAPIServer
	State      *state.State
	EtcdServer *etcdserver.EtcdServer
}

func (server *PeerAPIServer) GetPeers(ctx context.Context, req *pb.GetPeersRequest) (*pb.GetPeersResponse, error) {
	members := server.EtcdServer.Cluster().Members()
	peers := make([]*pb.Peer, len(members))
	for i, m := range members {
		id := strconv.FormatUint(uint64(m.ID), 10)
		peers[i] = &pb.Peer{
			Id:         &id,
			Name:       &m.Name,
			PeerUrls:   m.RaftAttributes.PeerURLs,
			ClientUrls: m.Attributes.ClientURLs,
		}
	}
	return &pb.GetPeersResponse{Peers: peers}, nil
}

func (server *PeerAPIServer) AddSelfPeer(ctx context.Context, req *pb.AddSelfPeerRequest) (*pb.AddSelfPeerResponse, error) {
	if len(req.AdvertisedUrls) == 0 {
		return nil, MissingAdvertiseUrlError
	}

	cert, err := utils.ExtractPeerCertificate(ctx)
	if err != nil {
		return nil, utils.AuthFailure
	}

	peerName := cert.Subject.CommonName
	now := time.Now()

	urls := make([]url.URL, len(req.AdvertisedUrls))
	for i, a := range req.AdvertisedUrls {
		u, err := url.Parse(a)
		if err != nil {
			return nil, InvalidAdvertiseUrlError
		}
		urls[i] = *u
	}

	_, err = server.EtcdServer.AddMember(
		ctx,
		*membership.NewMember(peerName, urls, "", &now),
	)
	if err != nil {
		log.Printf("Error: Failed to add peer: %v", err)
		return nil, utils.ServerError
	}

	return &pb.AddSelfPeerResponse{}, nil
}

func (server *PeerAPIServer) AddNode(ctx context.Context, req *pb.AddNodeRequest) (*pb.AddNodeResponse, error) {
	nodeKey := fmt.Appendf(nil, "%s/%s/%s", utils.NodesNamespace, *req.Datacenter, *req.Name)

	var implicit string
	switch *req.NodeType {
	case pb.NodeType_AGENT:
		implicit = utils.AgentCertificateOU
	case pb.NodeType_OBSERVER:
		implicit = utils.ObserverCertificateOU
	}

	node := schemas.NodeSchema{
		Name:       *req.Name,
		Datacenter: *req.Datacenter,
		Location:   *req.Location,
		Type:       implicit,
	}

	serializedNode, err := json.Marshal(node)
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	res, err := server.EtcdServer.Txn(ctx, &etcdserverpb.TxnRequest{
		Compare: []*etcdserverpb.Compare{{
			Result: etcdserverpb.Compare_EQUAL,
			Target: etcdserverpb.Compare_CREATE,
			Key:    nodeKey,
			TargetUnion: &etcdserverpb.Compare_CreateRevision{
				CreateRevision: int64(0),
			},
		}},
		Success: []*etcdserverpb.RequestOp{{
			Request: &etcdserverpb.RequestOp_RequestPut{
				RequestPut: &etcdserverpb.PutRequest{
					Key:   nodeKey,
					Value: serializedNode,
				},
			},
		}},
	})
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	if !res.Succeeded {
		return nil, AgentAlreadyExistsError
	}

	crt, key := utils.CreateNodeCrt(
		server.State,
		*req.Datacenter,
		*req.Name,
		implicit,
	)

	return &pb.AddNodeResponse{
		Certificate: crt,
		Key:         key,
	}, nil
}

func (server *PeerAPIServer) GetNodeCredentials(ctx context.Context, req *pb.GetNodeCredentialsRequest) (*pb.GetNodeCredentialsResponse, error) {
	node, err := utils.GetNodeSchema(ctx, server.EtcdServer, *req.Datacenter, *req.Name)
	if err != nil {
		log.Print(err.Error())
		return nil, utils.ServerError
	}

	crt, key := utils.CreateNodeCrt(
		server.State,
		node.Datacenter,
		node.Name,
		node.Type,
	)

	return &pb.GetNodeCredentialsResponse{
		Certificate: crt,
		Key:         key,
	}, nil
}

func NewPeerApiClient(clusterUrl string, state *state.State) pb.PeerAPIClient {
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	transportCred := credentials.NewTLS(&tls.Config{
		ServerName:   "registry.cluster.internal",
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{state.ServerKeyPair},
	})

	conn, err := grpc.NewClient(clusterUrl, grpc.WithTransportCredentials(transportCred))
	if err != nil {
		log.Fatalf("Failed to create grpc client: %v", err)
	}
	return pb.NewPeerAPIClient(conn)
}

func StartApiServer(config *config.Config, state *state.State, etcdServer *etcdserver.EtcdServer) {
	peerApiServer := PeerAPIServer{State: state, EtcdServer: etcdServer}

	cert, err := tls.LoadX509KeyPair(state.ServerCrtFile, state.ServerKeyFile)
	if err != nil {
		log.Fatal(err)
	}

	listenAddr := config.PeerAPIListenHost()
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	transportCred := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
	})

	grpcServer := grpc.NewServer(grpc.Creds(transportCred))
	pb.RegisterPeerAPIServer(grpcServer, &peerApiServer)

	go func() {
		err = grpcServer.Serve(lis)
		if err != nil {
			log.Fatalf("Failed to start peer API: %v", err)
		}
	}()

	log.Printf("Started peer api at https://%v", listenAddr)
}
