package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
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

	switch evt.Action {
	case events.ActionCreate, events.ActionStart:
		registerService(
			config,
			state,
			evt.Actor.ID,
		)
	case events.ActionRemove, events.ActionStop, events.ActionDie:
		name, found := evt.Actor.Attributes["name"]
		if !found {
			log.Println("Error: No name found while deregistering")
			return
		}

		service, found := evt.Actor.Attributes["ssle.service"]
		if !found {
			log.Println("Error: No service label found while deregistering")
			return
		}

		deleteService(
			config,
			state,
			service,
			name,
		)
	}
}

func registerService(
	config *config.Config,
	state *state.State,
	containerId string,
) {
	ctr, err := state.DockerClient.ContainerInspect(context.Background(), containerId)
	if err != nil {
		log.Printf("Error while retrieving container: %v\n", err)
		return
	}

	svc, found := ctr.Config.Labels["ssle.service"]
	if !found {
		log.Println("Container does not have service label")
		return
	}

	metricsPort := uint16(0)
	metrics, found := ctr.Config.Labels["ssle.metrics"]
	if found {
		parse, err := strconv.ParseUint(metrics, 10, 16)
		if err != nil {
			log.Printf("Error: Invalid metrics label for service: %s\n", err)
			return
		}
		metricsPort = uint16(parse)
	}

	ports := []schemas.PortSpec{}
	for port, bind := range ctr.NetworkSettings.Ports {
		parts := strings.SplitN(string(port), "/", 2)
		svcPort, _ := strconv.ParseUint(bind[0].HostPort, 10, 16)

		ports = append(ports, schemas.PortSpec{
			Name:     schemas.PathSegment(parts[0]),
			Port:     uint16(svcPort),
			Protocol: parts[1],
		})
	}

	container, _ := strings.CutPrefix(ctr.Name, "/")
	container = strings.ReplaceAll(container, "/", "_")
	spec := schemas.RegisterServiceRequest{
		Instance:    schemas.PathSegment(container),
		Addresses:   []schemas.Hostname{},
		Ports:       ports,
		MetricsPort: metricsPort,
	}

	res, err := state.RegistryPost(config, "/svc/"+url.PathEscape(svc), spec)
	if httpResponseError("Error registering service", res, err) {
		return
	}
}

func deleteService(
	config *config.Config,
	state *state.State,
	svc string,
	instance string,
) {
	container := strings.ReplaceAll(instance, "/", "_")
	path := fmt.Sprintf("/svc/%v/%v", url.PathEscape(svc), url.PathEscape(container))
	res, err := state.RegistryDelete(config, path)
	if httpResponseError("Error deregistering service", res, err) {
		return
	}
}

func cleanup(config *config.Config, state *state.State) {
	log.Println("Cleaning up")
	res, err := state.RegistryDelete(config, "/svc")
	httpResponseError("Error cleaning up", res, err)
	os.Exit(0)
}

func main() {
	config := config.LoadConfig()
	state := state.LoadState(&config)

	res, err := state.RegistryDelete(&config, "/svc")
	if httpResponseError("Error resetting node", res, err) {
		return
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cleanup(&config, &state)
	}()
	defer cleanup(&config, &state)

	args := filters.NewArgs(filters.Arg("label", "manager=ssle"))

	evtChan, errChan := state.DockerClient.Events(context.Background(), events.ListOptions{Filters: args})

	go func() {
		for {
			select {
			case evt := <-evtChan:
				HandleEvent(&config, &state, evt)
			case err := <-errChan:
				panic(err)
			}
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
			ctr.ID,
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
