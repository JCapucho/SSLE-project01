package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"codeberg.org/miekg/dns"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"

	"ssle/agent/config"
	"ssle/agent/state"
	pb "ssle/services"
)

type UnsignedImageEvent struct {
	Message string `json:"msg"`
	Image   string `json:"image"`
	Reason  string `json:"reason"`
}

func HandleEvent(
	state *state.State,
	evt events.Message,
) {
	switch evt.Type {
	case events.ContainerEventType:
		handleEventContainer(state, evt)
	case events.ImageEventType:
		handleEventImage(state, evt)
	}
}

func handleEventContainer(
	state *state.State,
	evt events.Message,
) {
	if evt.Actor.Attributes["manager"] != "ssle" {
		return
	}

	switch evt.Action {
	case events.ActionCreate, events.ActionStart:
		ctr, err := state.DockerClient.ContainerInspect(context.Background(), evt.Actor.ID)
		if err != nil {
			log.Printf("Error while retrieving container: %v\n", err)
			return
		}

		if checkImage(ctr.Image, state) {
			registerServiceFromContainer(state, &ctr)
		} else {
			err := state.DockerClient.ContainerRemove(context.Background(), evt.Actor.ID, container.RemoveOptions{Force: true})
			if err != nil {
				log.Printf("Failed to stop unsigned container: %v", err)
			}
			removeUnsignedImage(ctr.Image, state)
		}
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

		_, err := state.RegistryClient.Deregister(context.Background(), &pb.DeregisterServiceRequest{
			Service:  &service,
			Instance: &name,
		})
		if err != nil {
			log.Printf("Error deregistering service: %v", err)
			return
		}
	}
}

func handleEventImage(
	state *state.State,
	evt events.Message,
) {
	switch evt.Action {
	case events.ActionPull:
		if !checkImage(evt.Actor.ID, state) {
			removeUnsignedImage(evt.Actor.ID, state)
		}
	}
}

func checkImage(imageId string, state *state.State) bool {
	img, err := state.DockerClient.ImageInspect(context.Background(), imageId)
	if err != nil {
		log.Printf("Failed to inspect image: %v", err)
		return false
	}

	res, err := VerifyImageSignature(&img, state)
	if err != nil {
		log.Printf("Failed to verify image: %v", err)

		image := imageId
		if len(img.RepoTags) > 0 {
			image = img.RepoTags[0]
		}

		state.WriteEvent(UnsignedImageEvent{
			Message: "Unsigned image detected",
			Image:   image,
			Reason:  err.Error(),
		})

		return false
	}

	log.Printf(
		"Image %s signed by %s",
		imageId,
		res.Signature.Certificate.SubjectAlternativeName,
	)

	return true
}

func removeUnsignedImage(imageId string, state *state.State) {
	log.Print("Removing unsigned image")

	_, err := state.DockerClient.ImageRemove(context.Background(), imageId, image.RemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Printf("Failed to remove image: %v", err)
	}

}

func registerService(
	state *state.State,
	containerId string,
) {
	ctr, err := state.DockerClient.ContainerInspect(context.Background(), containerId)
	if err != nil {
		log.Printf("Error while retrieving container: %v\n", err)
		return
	}

	registerServiceFromContainer(state, &ctr)
}

func registerServiceFromContainer(
	state *state.State,
	ctr *container.InspectResponse,
) {
	svc, found := ctr.Config.Labels["ssle.service"]
	if !found {
		log.Println("Container does not have service label")
		return
	}

	metricsPort := uint32(0)
	metrics, found := ctr.Config.Labels["ssle.metrics"]
	if found {
		parse, err := strconv.ParseUint(metrics, 10, 16)
		if err != nil {
			log.Printf("Error: Invalid metrics label for service: %s\n", err)
			return
		}
		metricsPort = uint32(parse)
	}

	ports := []*pb.PortSpec{}
	for port, bind := range ctr.NetworkSettings.Ports {
		parts := strings.SplitN(string(port), "/", 2)

		rawPort := parts[0]
		if len(bind) > 0 {
			rawPort = bind[0].HostPort
		}
		svcPortRaw, _ := strconv.ParseUint(rawPort, 10, 16)
		svcPort := uint32(svcPortRaw)

		ports = append(ports, &pb.PortSpec{
			Name:     &parts[0],
			Port:     &svcPort,
			Protocol: &parts[1],
		})
	}

	container, _ := strings.CutPrefix(ctr.Name, "/")
	container = strings.ReplaceAll(container, "/", "_")
	req := &pb.RegisterServiceRequest{
		Service:     &svc,
		Instance:    &container,
		Addresses:   []string{},
		Ports:       ports,
		MetricsPort: &metricsPort,
	}

	_, err := state.RegistryClient.Register(context.Background(), req)
	if err != nil {
		log.Printf("Error registering service: %v", err)
		return
	}
}

func cleanup(state *state.State) {
	if r := recover(); r != nil {
		log.Println("Agent panicked:", r)
		debug.PrintStack()
	}

	log.Println("Cleaning up")

	_, err := state.RegistryClient.Reset(context.Background(), &pb.ResetRequest{})
	if err != nil {
		log.Printf("Error cleaning up: %v", err)
	}

	os.Exit(0)
}

func GetRegistryConfig(state *state.State) (*pb.ConfigResponse, error) {
	res, err := state.RegistryClient.Config(context.Background(), &pb.ConfigRequest{})
	if err != nil {
		return nil, err
	}

	if res.Certificate != nil && res.Key != nil {
		err = state.UpdateCredentials(res.Certificate, res.Key)
		if err != nil {
			log.Printf("Failed to update agent credentials: %v", err)
		}
	}

	state.UpdateAddrs(res.RegistryAddrs)

	return res, nil
}

func ConfigBackgroundJob(state *state.State) {
	for {
		_, err := GetRegistryConfig(state)
		if err != nil {
			log.Printf("Failed to refresh agent config: %v", err)
			continue
		}

		// Refresh config every minute
		time.Sleep(time.Minute)
	}
}

func HeartbeatBackgroundJob(state *state.State, period time.Duration) {
	for {
		_, err := state.RegistryClient.Heartbeat(context.Background(), &pb.HeartbeatRequest{})
		if err != nil {
			log.Printf("Failed to send heartbeat: %v", err)
			continue
		}

		// Refresh config every minute
		time.Sleep(period * time.Second)
	}
}

func StartupConsistencyJob(state *state.State) {
	args := filters.NewArgs(filters.Arg("label", "manager=ssle"))

	containers, err := state.DockerClient.ContainerList(context.Background(), container.ListOptions{Filters: args})
	if err != nil {
		panic(err)
	}

	for _, ctr := range containers {
		if checkImage(ctr.ImageID, state) {
			registerService(state, ctr.ID)
		} else {
			err := state.DockerClient.ContainerRemove(context.Background(), ctr.ID, container.RemoveOptions{Force: true})
			if err != nil {
				log.Printf("Failed to stop unsigned container: %v", err)
			}
			removeUnsignedImage(ctr.ImageID, state)
		}
	}
}

func main() {
	config := config.LoadConfig()
	state := state.LoadState(&config)

	_, err := state.RegistryClient.Reset(context.Background(), &pb.ResetRequest{})
	if err != nil {
		log.Printf("Error resetting node: %v", err)
		return
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		cleanup(state)
	}()
	defer cleanup(state)

	registryConfig, err := GetRegistryConfig(state)
	if err != nil {
		log.Fatalf("Failed to load registry config: %v", err)
	}

	// Start config background job
	go ConfigBackgroundJob(state)
	go HeartbeatBackgroundJob(state, time.Duration(*registryConfig.HeartbeatPeriod))

	evtChan, errChan := state.DockerClient.Events(context.Background(), events.ListOptions{})

	go func() {
		for {
			select {
			case evt := <-evtChan:
				HandleEvent(state, evt)
			case err := <-errChan:
				panic(err)
			}
		}
	}()

	go StartupConsistencyJob(state)

	mux := dns.NewServeMux()
	mux.Handle("cluster.internal.", &ClusterDnsHandler{
		config: &config,
		state:  state,
	})
	mux.Handle(".", NewForwardHandler(&config))

	addr := fmt.Sprintf("%v:53", config.DNSBindAddr)
	server := &dns.Server{
		Addr:    addr,
		Net:     "udp",
		Handler: mux,
		UDPSize: 65535,
	}

	log.Printf("Starting DNS server on %v\n", addr)
	err = server.ListenAndServe()
	if err != nil {
		fmt.Printf("Failed to start server: %s\n", err.Error())
	}
}
