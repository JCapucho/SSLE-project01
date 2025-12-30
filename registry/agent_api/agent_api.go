package agent_api

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"

	"go.etcd.io/etcd/server/v3/etcdserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"ssle/registry/config"
	"ssle/registry/state"
	pb "ssle/services"
)

type AgentAPIServer struct {
	pb.UnimplementedAgentAPIServer
	State      *state.State
	EtcdServer *etcdserver.EtcdServer
}

func StartApiServer(config *config.Config, state *state.State, etcdServer *etcdserver.EtcdServer) {
	nodeApiServer := NodeAPIServer{State: state, EtcdServer: etcdServer}
	agentApiServer := AgentAPIServer{State: state, EtcdServer: etcdServer}
	observerApiServer := ObserverAPIServer{State: state, EtcdServer: etcdServer}

	cert, err := tls.LoadX509KeyPair(state.ServerCrtFile, state.ServerKeyFile)
	if err != nil {
		log.Fatal(err)
	}

	listenAddr := config.AgentAPIListenHost()
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.AgentCA.Leaf)

	transportCred := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
	})

	grpcServer := grpc.NewServer(grpc.Creds(transportCred))
	pb.RegisterNodeAPIServer(grpcServer, &nodeApiServer)
	pb.RegisterAgentAPIServer(grpcServer, &agentApiServer)
	pb.RegisterObserverAPIServer(grpcServer, &observerApiServer)

	go func() {
		err = grpcServer.Serve(lis)
		if err != nil {
			log.Fatalf("Failed to start agent API: %v", err)
		}
	}()

	log.Printf("Started agent api at https://%v", listenAddr)
}
