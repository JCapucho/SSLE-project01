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
	"github.com/sigstore/sigstore-go/pkg/verify"

	"ssle/agent/config"
	agent_events "ssle/agent/events"
	"ssle/agent/state"
	pb "ssle/services"
)

func HandleEvent(
	state *state.State,
	evt events.Message,
) {
	switch evt.Type {
	case events.ContainerEventType:
		handleEventContainer(state, evt)
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

		if checkImage(&ctr, state) {
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

		_, err := state.AgentClient.Deregister(context.Background(), &pb.DeregisterServiceRequest{
			Service:  &service,
			Instance: &name,
		})
		if err != nil {
			log.Printf("Error deregistering service: %v", err)
			return
		}
	}
}

func checkImage(ctr *container.InspectResponse, state *state.State) bool {
	issuer := ctr.Config.Labels["ssle.issuer"]
	san := ctr.Config.Labels["ssle.san"]

	if issuer == "" {
		log.Printf("Container %s has no signature verification", ctr.Name)
		state.WriteEvent(agent_events.NewNoSignatureConfigurationEvent(ctr.Name))
		return true
	}

	certId, err := verify.NewShortCertificateIdentity(issuer, "", "", san)
	if err != nil {
		log.Printf("Invalid signature verification configuration: %v", err)
		return false
	}

	img, err := state.DockerClient.ImageInspect(context.Background(), ctr.Image)
	if err != nil {
		log.Printf("Failed to inspect image: %v", err)
		return false
	}

	image := ctr.Image
	if len(img.RepoTags) > 0 {
		image = img.RepoTags[0]
	}

	res, err := VerifyImageSignature(certId, &img, state)
	if err != nil {
		log.Printf("Failed to verify image: %v", err)
		state.WriteEvent(agent_events.NewUnsignedImageEvent(image, err.Error()))
		return false
	}

	log.Printf(
		"Image %s signed by %s",
		image,
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

	_, err := state.AgentClient.Register(context.Background(), req)
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

	_, err := state.AgentClient.Reset(context.Background(), &pb.ResetRequest{})
	if err != nil {
		log.Printf("Error cleaning up: %v", err)
	}

	os.Exit(0)
}

func StartupConsistencyJob(state *state.State) {
	args := filters.NewArgs(filters.Arg("label", "manager=ssle"))

	containers, err := state.DockerClient.ContainerList(context.Background(), container.ListOptions{Filters: args})
	if err != nil {
		panic(err)
	}

	for _, ctrListing := range containers {
		ctr, err := state.DockerClient.ContainerInspect(context.Background(), ctrListing.ID)
		if err != nil {
			log.Printf("Error while retrieving container: %v\n", err)
			return
		}

		if checkImage(&ctr, state) {
			registerServiceFromContainer(state, &ctr)
		} else {
			err := state.DockerClient.ContainerRemove(context.Background(), ctr.ID, container.RemoveOptions{Force: true})
			if err != nil {
				log.Printf("Failed to stop unsigned container: %v", err)
			}
			removeUnsignedImage(ctr.Image, state)
		}
	}
}

func main() {
	config := config.LoadConfig()
	state := state.LoadState(&config)

	_, err := state.AgentClient.Reset(context.Background(), &pb.ResetRequest{})
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

	registryConfig, err := state.GetRegistryConfig()
	if err != nil {
		log.Fatalf("Failed to load registry config: %v", err)
	}

	// Start config background job
	go state.ConfigBackgroundJob()
	go state.HeartbeatBackgroundJob(time.Duration(*registryConfig.HeartbeatPeriod))

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
