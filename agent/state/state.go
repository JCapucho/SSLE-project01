package state

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"ssle/agent/config"

	dockerClient "github.com/docker/docker/client"
)

type State struct {
	RegistryClient *http.Client
	DockerClient   *dockerClient.Client
}

func LoadState(config *config.Config) State {
	CAPem, err := os.ReadFile(config.CAFile)
	if err != nil {
		log.Fatalf("Failed to read CA certificate: %v", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(CAPem)

	regClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}

	dcli, err := dockerClient.NewClientWithOpts(dockerClient.FromEnv, dockerClient.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}

	return State{
		RegistryClient: regClient,
		DockerClient:   dcli,
	}
}

func (state *State) NewRegistryRequest(
	config *config.Config,
	method string,
	path string,
	body io.Reader,
) (*http.Request, error) {
	reqUrl := *config.JoinUrl
	reqUrl.Path = path

	req, err := http.NewRequest("GET", reqUrl.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+config.Token)

	return req, nil
}

func (state *State) RegistryGet(config *config.Config, path string) (*http.Response, error) {
	req, err := state.NewRegistryRequest(config, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	res, err := state.RegistryClient.Do(req)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (state *State) RegistryPost(config *config.Config, path string, v any) (*http.Response, error) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("Error while building JSON body: %v", err)
		return nil, err
	}

	req, err := state.NewRegistryRequest(config, "POST", path, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	res, err := state.RegistryClient.Do(req)
	if err != nil {
		return nil, err
	}

	return res, nil
}
