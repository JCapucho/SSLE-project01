package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"codeberg.org/miekg/dns"
	"codeberg.org/miekg/dns/dnsconf"

	"ssle/schemas"

	"ssle/agent/config"
	"ssle/agent/state"
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

		queryPath := "/svc"
		for _, v := range slices.Backward(parts) {
			queryPath = fmt.Sprintf("%v/%v", queryPath, url.PathEscape(v))
		}

		res, err := h.state.RegistryGet(h.config, queryPath)
		if httpResponseError("Error obtaining service", res, err) {
			r.MsgHeader.Rcode = dns.RcodeNameError
			break
		}

		var specs []schemas.ServiceSpec
		err = json.NewDecoder(res.Body).Decode(&specs)
		if err != nil {
			r.MsgHeader.Rcode = dns.RcodeNameError
			log.Printf("Error obtaining service: %v\n", err)
			break
		}

		for _, spec := range specs {
			for _, addr := range spec.Addresses {
				if addr.IsAddress() {
					ipAddr := addr.Address()
					if ipAddr.Is4() && header.Class == dns.TypeA {
						answers = append(answers, &dns.A{
							Hdr: dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 30},
							A:   ipAddr.AsSlice(),
						})
					} else if ipAddr.Is6() && header.Class == dns.TypeA {
						answers = append(answers, &dns.AAAA{
							Hdr:  dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 30},
							AAAA: ipAddr.AsSlice(),
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
