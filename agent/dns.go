package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/netip"
	"strings"
	"time"

	"codeberg.org/miekg/dns"
	"codeberg.org/miekg/dns/dnsconf"

	"ssle/agent/config"
	"ssle/agent/state"
	pb "ssle/services"
)

type ClusterDnsHandler struct {
	config *config.Config
	state  *state.State
}

func (h *ClusterDnsHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) {
	// re-use r
	r.MsgHeader.Authoritative = true
	r.Answer, r.Ns, r.Extra, r.Pseudo = nil, nil, nil, nil
	r.Response = true

	answers := []dns.RR{}

	for _, question := range r.Question {
		header := question.Header()
		path, found := strings.CutSuffix(header.Name, ".cluster.internal.")

		if !found {
			r.MsgHeader.Rcode = dns.RcodeNameError
		}

		if header.Class != dns.TypeA && header.Class != dns.TypeAAAA {
			continue
		}

		parts := strings.Split(path, ".")
		if len(parts) < 1 || len(parts) > 5 {
			r.MsgHeader.Rcode = dns.RcodeNameError
			break
		}

		queryLastIdx := len(parts) - 1

		req := &pb.DiscoverRequest{
			Service: &parts[queryLastIdx],
		}
		if queryLastIdx > 0 {
			req.Location = &parts[queryLastIdx-1]
		}
		if queryLastIdx > 1 {
			req.Datacenter = &parts[queryLastIdx-2]
		}
		if queryLastIdx > 3 {
			req.Node = &parts[queryLastIdx-3]
		}
		if queryLastIdx > 4 {
			req.Instance = &parts[queryLastIdx-4]
		}

		res, err := h.state.RegistryClient.Discover(ctx, req)
		if err != nil {
			log.Printf("Error obtaining service: %v", err)
			r.MsgHeader.Rcode = dns.RcodeNameError
			break
		}

		for _, spec := range res.Services {
			for _, addr := range spec.Addresses {
				ip, err := netip.ParseAddr(addr)
				if err == nil {
					if ip.Is4() && header.Class == dns.TypeA {
						answers = append(answers, &dns.A{
							Hdr: dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 30},
							A:   ip.AsSlice(),
						})
					} else if ip.Is6() && header.Class == dns.TypeA {
						answers = append(answers, &dns.AAAA{
							Hdr:  dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 30},
							AAAA: ip.AsSlice(),
						})
					}
				} else {
					log.Println("Error: Hostnames not supported")
				}
			}
		}
	}

	if r.MsgHeader.Rcode == 0 {
		r.Answer = answers
	}

	r.Pack()
	io.Copy(w, r)
}

type ForwardDnsHandler struct {
	config    *config.Config
	dnsConfig *dnsconf.Config
	client    *dns.Client
}

func NewForwardHandler(config *config.Config) *ForwardDnsHandler {
	dnsConfig, err := dnsconf.FromFile("/etc/resolv.conf")
	if err != nil {
		log.Fatalf("Failed to read DNS configuration: %v", err)
	}

	client := dns.NewClient()
	client.Transport.ReadTimeout = time.Duration(dnsConfig.Timeout) * time.Second
	client.Transport.WriteTimeout = time.Duration(dnsConfig.Timeout) * time.Second

	return &ForwardDnsHandler{
		config:    config,
		dnsConfig: dnsConfig,
		client:    client,
	}
}

func (h *ForwardDnsHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) {
	for _, server := range h.dnsConfig.Servers {
		for range h.dnsConfig.Attempts {
			addr := fmt.Sprintf("%v:%v", server, h.dnsConfig.Port)
			resp, _, err := h.client.Exchange(ctx, r, "udp", addr)

			if err != nil {
				log.Printf("DNS error: %v\n", err)
				continue
			}

			resp.Pack()
			io.Copy(w, resp)
			return
		}
	}
}
