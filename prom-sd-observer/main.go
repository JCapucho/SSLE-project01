package main

import (
	"context"
	"log"
	"time"

	"ssle/prom-sd-observer/config"
	"ssle/prom-sd-observer/state"
	"ssle/services"
)

func main() {
	config := config.LoadConfig()
	state := state.LoadState(&config)

	registryConfig, err := state.GetRegistryConfig()
	if err != nil {
		log.Fatalf("Failed to load registry config: %v", err)
	}

	// Start config background job
	go state.ConfigBackgroundJob()
	go state.HeartbeatBackgroundJob(time.Duration(*registryConfig.HeartbeatPeriod))

	stream, err := state.ObserverClient.WatchDatacenterServices(context.Background(), &services.WatchDatacenterServicesRequest{})
	if err != nil {
		log.Fatalf("Failed to start watching datacenter services: %v", err)
	}

	// Fetch all existing services
	go func() {
		res, err := state.ObserverClient.GetDatacenterServices(context.Background(), &services.GetDatacenterServicesRequest{})
		if err != nil {
			log.Fatalf("Failed to fetch datacenter services: %v", err)
		}

		state.UpdateServiceBulk(res.Services)
	}()

	log.Print("Started prometheus service discovery observer")

	for {
		event, err := stream.Recv()
		if err != nil {
			log.Fatalf("Error watching datacenter services: %v", err)
		}

		switch not := event.Notification.(type) {
		case *services.WatchDatacenterServicesResponse_Update:
			state.UpdateService(not.Update.Service)
		case *services.WatchDatacenterServicesResponse_Delete:
			state.DeleteService(*not.Delete.Node, *not.Delete.ServiceName, *not.Delete.Instance)
		}
	}
}
