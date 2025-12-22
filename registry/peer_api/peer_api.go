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

	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.etcd.io/etcd/server/v3/etcdserver/api/membership"

	"ssle/registry/config"
	"ssle/registry/state"
	"ssle/registry/utils"
)

type PeerAPIState struct {
	state      *state.State
	etcdServer *etcdserver.EtcdServer
}

type AddPeerRequest struct {
	AdvertisedURLS []url.URL `json:"advertisedURLs"`
}

func (state PeerAPIState) listPeerHandler(w http.ResponseWriter, r *http.Request) {
	utils.HttpRespondJson(w, http.StatusOK, state.etcdServer.Cluster().Members())
}

func (state PeerAPIState) addPeerHandler(w http.ResponseWriter, r *http.Request) {
	addPeerReq, err := utils.DeserializeRequestBody[AddPeerRequest](w, r)
	if err != nil {
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
		log.Printf("Error: Failed to add peer: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func StartPeerAPIHTTPServer(config *config.Config, state *state.State, etcdServer *etcdserver.EtcdServer) {
	apiState := PeerAPIState{
		state:      state,
		etcdServer: etcdServer,
	}

	handler := http.NewServeMux()
	// Peer endpoints
	handler.HandleFunc("GET /peers", apiState.listPeerHandler)
	handler.HandleFunc("POST /peers/add", apiState.addPeerHandler)
	// Node endpoints
	handler.HandleFunc("POST /node", apiState.addNodeHandler)

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	server := &http.Server{
		Addr:    config.PeerAPIListenHost(),
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

func createHTTPClient(state state.State) *http.Client {
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(state.CA.Leaf)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:   "registry.cluster.internal",
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{state.ServerKeyPair},
			},
		},
	}
}

func ClusterRequestAddPeer(clusterUrl url.URL, config config.Config, state state.State) error {
	client := createHTTPClient(state)

	url := clusterUrl
	url.Path = "/peers/add"

	body, err := json.Marshal(AddPeerRequest{
		AdvertisedURLS: config.EtcdAdvertiseURLs(),
	})
	if err != nil {
		return err
	}

	resp, err := client.Post(url.String(), utils.ContentTypeJSON, bytes.NewReader(body))
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
	client := createHTTPClient(state)

	url := clusterUrl
	url.Path = "/peers"

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
