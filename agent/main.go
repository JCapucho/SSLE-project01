package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"codeberg.org/miekg/dns"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"

	"ssle/schemas"

	"ssle/agent/config"
	"ssle/agent/state"
)

func httpResponseError(msg string, res *http.Response, err error) bool {
	if err != nil {
		log.Printf("%s: %v\n", msg, err)
		return true
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, err := io.ReadAll(res.Body)
		if err == nil {
			log.Printf("%s (status code: %v): %v\n", msg, res.StatusCode, string(body))
		} else {
			log.Printf("%s (status code: %v)\n", msg, res.StatusCode)
		}

		return true
	}

	return false
}

func HandleEvent(
	config *config.Config,
	state *state.State,
	evt events.Message,
) {
	if evt.Type != events.ContainerEventType {
		return
	}

	if evt.Action != events.ActionCreate {
		return
	}

	res, err := state.DockerClient.ContainerInspect(context.Background(), evt.Actor.ID)
	if err != nil {
		log.Printf("Error while retrieving container: %v", err)
		return
	}

	registerService(
		config,
		state,
		res.Config.Labels["ssle.service"],
		res.Name,
	)
}

func registerService(
	config *config.Config,
	state *state.State,
	svc string,
	container string,
) {
	container = strings.ReplaceAll(container, "/", "_")
	spec := schemas.RegisterServiceRequest{
		Instance:  schemas.PathSegment(container),
		Addresses: []schemas.Hostname{},
		Ports:     []schemas.PortSpec{},
	}

	res, err := state.RegistryPost(config, "/svc/"+url.PathEscape(svc), spec)
	if httpResponseError("Error registering service", res, err) {
		return
	}
}

func main() {
	config := config.LoadConfig()
	state := state.LoadState(&config)

	args := filters.NewArgs(filters.Arg("label", "manager=ssle"))

	evtChan, errChan := state.DockerClient.Events(context.Background(), events.ListOptions{Filters: args})

	go func() {
		select {
		case evt := <-evtChan:
			HandleEvent(&config, &state, evt)
		case err := <-errChan:
			panic(err)
		}
	}()

	containers, err := state.DockerClient.ContainerList(context.Background(), container.ListOptions{Filters: args})
	if err != nil {
		panic(err)
	}

	for _, ctr := range containers {
		registerService(
			&config,
			&state,
			ctr.Labels["ssle.service"],
			ctr.Names[0],
		)
	}

	mux := dns.NewServeMux()
	mux.Handle("cluster.local.", &ClusterDnsHandler{
		config: &config,
		state:  &state,
	})
	mux.Handle(".", &ForwardDnsHandler{
		client: dns.NewClient(),
	})

	server := &dns.Server{
		Addr:    "127.0.0.143:53",
		Net:     "udp",
		Handler: mux,
		UDPSize: 65535,
	}

	fmt.Println("Starting DNS server on 127.0.0.143")
	err = server.ListenAndServe()
	if err != nil {
		fmt.Printf("Failed to start server: %s\n", err.Error())
	}
}
