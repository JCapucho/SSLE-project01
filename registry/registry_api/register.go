package registry_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"ssle/registry/schema"
	"ssle/registry/utils"

	"go.etcd.io/etcd/pkg/v3/traceutil"
	"go.etcd.io/etcd/server/v3/lease"
)

type RegisterServiceRequest struct {
	Instance schema.PathSegment `json:"instance" validate:"required"`

	Addresses   []schema.Hostname `json:"addrs"`
	Ports       []schema.PortSpec `json:"ports"`
	MetricsPort uint16            `json:"metricsPort"`
}

func (spec *RegisterServiceRequest) UnmarshalJSON(data []byte) error {
	type RawRequest RegisterServiceRequest
	raw := RawRequest{
		Addresses: []schema.Hostname{},
		Ports:     []schema.PortSpec{},
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*spec = RegisterServiceRequest(raw)
	return nil
}

func (state RegistryAPIState) registerService(w http.ResponseWriter, r *http.Request) {
	dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	regSvcReq, err := utils.DeserializeRequestBody[RegisterServiceRequest](w, r)
	if err != nil {
		return
	}

	svc := r.PathValue("service")
	spec := schema.ServiceSpec{
		ServiceName: schema.PathSegment(svc),
		Instance:    regSvcReq.Instance,

		Location:   schema.PathSegment(location),
		DataCenter: schema.PathSegment(dc),

		Addresses:   regSvcReq.Addresses,
		Ports:       regSvcReq.Ports,
		MetricsPort: regSvcReq.MetricsPort,
	}

	if len(spec.Addresses) == 0 {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			log.Print(err.Error())
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		hostname, err := schema.ParseHostname(ip)
		if err != nil {
			log.Print(err.Error())
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		spec.Addresses = []schema.Hostname{hostname}
	}

	svcKey := fmt.Sprintf(
		"%v/%v/%v/%v/%v",
		utils.ServiceNamespace,
		spec.ServiceName,
		location,
		dc,
		spec.Instance,
	)

	serializedSpec, err := json.Marshal(spec)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	var serializedPrometheusService []byte
	prometheusService := schema.PrometheusServiceFromServiceSpec(&spec)
	if prometheusService != nil {
		serializedPrometheusService, err = json.Marshal(prometheusService)
		if err != nil {
			log.Print(err.Error())
			http.Error(w, "", http.StatusInternalServerError)
			return
		}
	}

	kv := state.etcdServer.KV()

	tx := kv.Write(traceutil.New("Register service", state.etcdServer.Logger()))
	tx.Put([]byte(svcKey), serializedSpec, lease.NoLease)
	if serializedPrometheusService != nil {
		dsSvcKey := fmt.Sprintf(
			"%v/%v/%v/%v",
			utils.DCServicesNamespace,
			dc,
			spec.ServiceName,
			spec.Instance,
		)
		tx.Put([]byte(dsSvcKey), serializedPrometheusService, lease.NoLease)
	}
	tx.End()

	kv.Commit()

	utils.HttpRespondJson(w, http.StatusOK, spec)
}
