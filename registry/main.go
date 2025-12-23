package main

import (
	"context"
	"log"
	"strconv"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/types"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"

	"ssle/registry/agent_api"
	"ssle/registry/config"
	"ssle/registry/etcd"
	"ssle/registry/peer_api"
	"ssle/registry/state"
	"ssle/services"
)

func main() {
	var err error

	config := config.LoadConfig()
	state := state.LoadState(config)
	peer_api_client := peer_api.NewPeerApiClient(config.JoinUrl, &state)

	if config.JoinUrl != "" {
		urls := make([]string, len(config.EtcdAdvertiseURLs()))
		for i, u := range config.EtcdAdvertiseURLs() {
			urls[i] = u.String()
		}
		_, err := peer_api_client.AddSelfPeer(context.Background(), &services.AddSelfPeerRequest{
			AdvertisedUrls: urls,
		})
		if err != nil {
			log.Printf("Failed to join cluster: %v", err)
			log.Print("Continuing with registry startup, server may crash")
		}
	}

	members := []membership.Member{}
	if config.JoinUrl != "" {
		res, err := peer_api_client.GetPeers(context.Background(), &services.GetPeersRequest{})
		log.Printf("Existing cluster members: %v", members)
		if err != nil {
			log.Fatalf("Failed to get cluster members: %v", err)
		}

		members = make([]membership.Member, len(res.Peers))
		for i, peer := range res.Peers {
			id, err := strconv.ParseUint(*peer.Id, 10, 64)
			if err != nil {
				log.Fatal(err.Error())
			}
			members[i] = membership.Member{
				ID: types.ID(id),
				RaftAttributes: membership.RaftAttributes{
					PeerURLs:  peer.PeerUrls,
					IsLearner: false,
				},
				Attributes: membership.Attributes{
					ClientURLs: peer.ClientUrls,
					Name:       *peer.Name,
				},
			}
		}
	}

	etcdConfig := etcd.CreateEtcdConfig(members, &state, &config)

	// Register our extensions into etcd client listener
	e, err := embed.StartEtcd(etcdConfig)
	if err != nil {
		log.Fatalf("Failed to start etcd server: %v", err)
	}
	defer e.Close()

	peer_api.StartApiServer(&config, &state, e.Server)

	select {
	case <-e.Server.ReadyNotify():
		log.Print("Server is ready!")
		etcd.EtcdPostStartUpdate(&config, e)

		agent_api.StartApiServer(&config, &state, e.Server)
	case <-time.After(60 * time.Second):
		e.Server.Stop() // trigger a shutdown
		log.Print("Server took too long to start!")
	}

	log.Fatal(<-e.Err())
}
