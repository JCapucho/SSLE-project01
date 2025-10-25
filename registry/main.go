package main

import (
	"log"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"

	"ssle/registry/config"
	"ssle/registry/etcd"
	"ssle/registry/peer_api"
	"ssle/registry/registry_api"
	"ssle/registry/state"
)

func main() {
	var err error

	config := config.LoadConfig()
	state := state.LoadState(config)

	if config.JoinUrl != nil {
		err := peer_api.ClusterRequestAddPeer(*config.JoinUrl, config, state)
		if err != nil {
			log.Printf("Failed to join cluster: %v", err)
			log.Print("Continuing with registry startup, server may crash")
		}
	}

	members := []membership.Member{}
	if config.JoinUrl != nil {
		members, err = peer_api.ClusterRequestGetPeers(*config.JoinUrl, config, state)
		if err != nil {
			log.Fatalf("Failed to get cluster members: %v", err)
		}
	}
	etcdConfig := etcd.CreateEtcdConfig(members, &state, &config)

	e, err := embed.StartEtcd(etcdConfig)
	if err != nil {
		log.Fatalf("Failed to start etcd server: %v", err)
	}
	defer e.Close()

	peer_api.StartPeerAPIHTTPServer(&config, &state, e.Server)
	registry_api.StartRegistryAPIHTTPServer(&config, &state, e.Server)

	select {
	case <-e.Server.ReadyNotify():
		log.Printf("Server is ready!")
		etcd.EtcdPostStartUpdate(e)
	case <-time.After(60 * time.Second):
		e.Server.Stop() // trigger a shutdown
		log.Printf("Server took too long to start!")
	}

	log.Fatal(<-e.Err())
}
