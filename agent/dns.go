package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/url"
	"strings"

	"codeberg.org/miekg/dns"

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

	answers := []dns.RR{}

	for _, question := range r.Question {
		header := question.Header()
		path, found := strings.CutSuffix(header.Name, "cluster.local.")

		if !found {
			r.MsgHeader.Rcode = dns.RcodeNameError
		}

		if header.Class != dns.TypeA && header.Class != dns.TypeAAAA {
			continue
		}

		parts := strings.Split(path, ".")
		if len(parts) < 1 || len(parts) > 3 {
			r.MsgHeader.Rcode = dns.RcodeNameError
			break
		}

		res, err := h.state.RegistryGet(h.config, "/svc/"+url.PathEscape(parts[0]))
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
				if err != nil {
					log.Printf("Error while parsing address: %v\n", err)
					continue
				}

				if addr.IsAddress() {
					ipAddr := addr.Address()
					if ipAddr.Is4() && header.Class == dns.TypeA {
						answers = append(answers, &dns.A{
							Hdr: dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 0},
							A:   ipAddr.AsSlice(),
						})
					} else if ipAddr.Is6() && header.Class == dns.TypeA {
						answers = append(answers, &dns.AAAA{
							Hdr:  dns.Header{Name: header.Name, Class: dns.ClassINET, TTL: 0},
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
	client *dns.Client
}

func (h *ForwardDnsHandler) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) {
	resp, _, err := h.client.Exchange(ctx, r, "udp", "192.168.227.21:53")

	if err != nil {
		log.Printf("DNS error: %v\n", err)
		return
	}

	resp.Pack()
	io.Copy(w, resp)
}
