package etcd

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"

	"ssle/registry/config"
	"ssle/registry/state"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	"go.etcd.io/etcd/server/v3/embed"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"
)

func EtcdPostStartUpdate(etcd *embed.Etcd) {
	PeerURLs := make([]string, len(etcd.Config().AdvertisePeerUrls))
	for i, url := range etcd.Config().AdvertisePeerUrls {
		PeerURLs[i] = url.String()
	}

	_, err := etcd.Server.UpdateMember(
		context.TODO(),
		membership.Member{
			ID: etcd.Server.MemberID(),
			RaftAttributes: membership.RaftAttributes{
				PeerURLs:  PeerURLs,
				IsLearner: false,
			},
		},
	)
	if err != nil {
		log.Printf("Failed to update member URLs: %v", err)
	}
}

func CreateEtcdConfig(members []membership.Member, state state.State, config config.Config) *embed.Config {
	etcdToken, err := hkdf.Expand(sha256.New, state.Token, "etcd", 32)
	if err != nil {
		panic(err.Error())
	}

	etcdCfg := embed.NewConfig()
	etcdCfg.Name = config.Name
	etcdCfg.Dir = state.EtcdDir
	etcdCfg.InitialClusterToken = base64.StdEncoding.EncodeToString(etcdToken)

	etcdCfg.PeerTLSInfo = transport.TLSInfo{
		CertFile:       state.ServerCrtFile,
		KeyFile:        state.ServerKeyFile,
		ClientCertAuth: true,
		TrustedCAFile:  state.CACrtFile,
	}

	etcdCfg.ListenPeerUrls = config.EtcdAdvertiseURLs()
	etcdCfg.AdvertisePeerUrls = etcdCfg.ListenPeerUrls

	// Disable client endpoints
	etcdCfg.ListenClientUrls = []url.URL{}
	etcdCfg.ListenClientHttpUrls = []url.URL{}
	etcdCfg.AdvertiseClientUrls = []url.URL{}

	etcdCfg.InitialCluster = etcdCfg.InitialClusterFromName(config.Name)
	for _, member := range members {
		if member.Name == config.Name {
			continue
		}

		for _, url := range member.PeerURLs {
			etcdCfg.InitialCluster += fmt.Sprintf(",%v=%v", member.Name, url)
		}
	}

	if len(members) == 0 {
		etcdCfg.ClusterState = embed.ClusterStateFlagNew
	} else {
		etcdCfg.ClusterState = embed.ClusterStateFlagExisting
	}

	return etcdCfg
}
