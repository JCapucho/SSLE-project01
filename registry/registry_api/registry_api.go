package registry_api

import (
	"encoding/pem"
	"errors"
	"log"
	"net/http"
	"strings"

	"ssle/registry/config"
	"ssle/registry/state"
	"ssle/registry/utils"

	"aidanwoods.dev/go-paseto"
	"go.etcd.io/etcd/server/v3/etcdserver"
)

type RegistryAPIState struct {
	state      *state.State
	etcdServer *etcdserver.EtcdServer
}

func (state RegistryAPIState) verifyToken(r *http.Request, implicit []byte) (*paseto.Token, error) {
	rawToken := r.Header.Get(utils.TokenHeader)
	if rawToken == "" {
		return nil, errors.New("Authorization header must be set")
	}
	rawToken, foundType := strings.CutPrefix(rawToken, "Bearer ")
	if !foundType {
		return nil, errors.New("Authorization header does not begin with Bearer type")
	}

	token, err := paseto.NewParser().ParseV4Local(state.state.TokenKey, rawToken, implicit)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (state RegistryAPIState) extractNameLocation(r *http.Request) (string, string, string, error) {
	token, err := state.verifyToken(r, []byte("DC"))
	if err != nil {
		return "", "", "", err
	}

	name, err := utils.ExtractTokenKey[string](token, "name")
	if err != nil {
		return "", "", "", err
	}
	dc, err := utils.ExtractTokenKey[string](token, "dc")
	if err != nil {
		return "", "", "", err
	}
	location, err := utils.ExtractTokenKey[string](token, "loc")
	if err != nil {
		return "", "", "", err
	}

	return name, dc, location, nil
}

type ConfigResponse struct {
	CACrt      string `json:"caCrt"`
	Name       string `json:"name"`
	Datacenter string `json:"dc"`
	Location   string `json:"location"`
}

func (state RegistryAPIState) config(w http.ResponseWriter, r *http.Request) {
	name, dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	CACrt := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: state.state.CA.Leaf.Raw,
	})

	utils.HttpRespondJson(w, http.StatusOK, ConfigResponse{
		CACrt:      string(CACrt),
		Name:       name,
		Datacenter: dc,
		Location:   location,
	})
}

func StartRegistryAPIHTTPServer(config *config.Config, state *state.State, etcdServer *etcdserver.EtcdServer) {
	apiState := RegistryAPIState{
		state:      state,
		etcdServer: etcdServer,
	}

	handler := http.NewServeMux()
	handler.HandleFunc("GET /config", apiState.config)
	handler.HandleFunc("POST /svc/{service}", apiState.registerService)

	handler.HandleFunc("GET /svc/{service}", apiState.getService)
	handler.HandleFunc("GET /svc/{service}/{location}", apiState.getService)
	handler.HandleFunc("GET /svc/{service}/{location}/{datacenter}", apiState.getService)
	handler.HandleFunc("GET /svc/{service}/{location}/{datacenter}/{node}", apiState.getService)
	handler.HandleFunc("GET /svc/{service}/{location}/{datacenter}/{node}/{instance}", apiState.getService)

	handler.HandleFunc("DELETE /svc", apiState.deleteNodeServices)
	handler.HandleFunc("DELETE /svc/{service}/{instance}", apiState.deleteServiceInstance)
	handler.HandleFunc("GET /discovery", apiState.prometheusDiscovery)

	server := &http.Server{
		Addr:    config.RegistryAPIListenHost(),
		Handler: handler,
	}

	go func() {
		log.Printf("Starting Registry API HTTP Server at %v", server.Addr)
		log.Fatal(server.ListenAndServeTLS(state.ServerCrtFile, state.ServerKeyFile))
	}()
}
