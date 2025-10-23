package peer_api

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"ssle/registry/config"
	"ssle/registry/state"

	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"
)

const (
	ContentTypeJSON = "application/json"
)

type PeerAPIState struct {
	etcdServer *etcdserver.EtcdServer
}

type AddPeerRequest struct {
	AdvertisedURLS []url.URL `json:"advertisedURLs"`
}

func (state PeerAPIState) listPeerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(state.etcdServer.Cluster().Members())
}

func (state PeerAPIState) addPeerHandler(w http.ResponseWriter, r *http.Request) {
	var addPeerReq AddPeerRequest

	err := json.NewDecoder(r.Body).Decode(&addPeerReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(addPeerReq.AdvertisedURLS) == 0 {
		http.Error(w, "At least one advertised URL must be set", http.StatusBadRequest)
		return
	}

	peerName := r.TLS.PeerCertificates[0].Subject.CommonName
	now := time.Now()

	_, err = state.etcdServer.AddMember(
		r.Context(),
		*membership.NewMember(peerName, addPeerReq.AdvertisedURLS, "", &now),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	w.WriteHeader(http.StatusCreated)
}

func StartPeerAPIHTTPServer(config config.Config, state state.State, etcdServer *etcdserver.EtcdServer) {
	apiState := PeerAPIState{
		etcdServer: etcdServer,
	}

	handler := http.NewServeMux()
	handler.HandleFunc("GET /members", apiState.listPeerHandler)
	handler.HandleFunc("POST /add", apiState.addPeerHandler)

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	server := &http.Server{
		Addr:    config.PeerAPIAdvertiseHost(),
		Handler: handler,
		TLSConfig: &tls.Config{
			ClientCAs:  caCertPool,
			ClientAuth: tls.RequireAndVerifyClientCert,
		},
	}

	go func() {
		log.Printf("Starting Peer API HTTP Server at %v", server.Addr)
		log.Fatal(server.ListenAndServeTLS(state.ServerCrtFile, state.ServerKeyFile))
	}()
}

func createHTTPClient(config config.Config, state state.State) *http.Client {
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{state.ServerKeyPair},
			},
		},
	}
}

func ClusterRequestAddPeer(clusterUrl url.URL, config config.Config, state state.State) error {
	client := createHTTPClient(config, state)

	url := clusterUrl
	url.Path = "/add"

	body, err := json.Marshal(AddPeerRequest{
		AdvertisedURLS: config.EtcdAdvertiseURLs(),
	})
	if err != nil {
		return err
	}

	resp, err := client.Post(url.String(), ContentTypeJSON, bytes.NewReader(body))
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		bodyString := string(bodyBytes)

		return fmt.Errorf("Add peer request failed (status code %v): %v", resp.StatusCode, bodyString)
	}

	return nil
}

func ClusterRequestGetPeers(clusterUrl url.URL, config config.Config, state state.State) ([]membership.Member, error) {
	client := createHTTPClient(config, state)

	url := clusterUrl
	url.Path = "/members"

	resp, err := client.Get(url.String())
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		bodyString := string(bodyBytes)

		return nil, fmt.Errorf("Get peers request failed (status code %v): %v", resp.StatusCode, bodyString)
	}

	var members []membership.Member
	err = json.NewDecoder(resp.Body).Decode(&members)
	if err != nil {
		return nil, err
	}

	return members, nil
}
