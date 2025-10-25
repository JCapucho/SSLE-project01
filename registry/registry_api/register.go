package registry_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"go.etcd.io/etcd/pkg/v3/traceutil"
	"go.etcd.io/etcd/server/v3/lease"

	"ssle/schemas"

	"ssle/registry/utils"
)

func (state RegistryAPIState) registerService(w http.ResponseWriter, r *http.Request) {
	dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	regSvcReq, err := utils.DeserializeRequestBody[schemas.RegisterServiceRequest](w, r)
	if err != nil {
		return
	}

	svc := r.PathValue("service")
	spec := schemas.ServiceSpec{
		ServiceName: schemas.PathSegment(svc),
		Instance:    regSvcReq.Instance,

		Location:   schemas.PathSegment(location),
		DataCenter: schemas.PathSegment(dc),

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

		hostname, err := schemas.ParseHostname(ip)
		if err != nil {
			log.Print(err.Error())
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		spec.Addresses = []schemas.Hostname{hostname}
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
	prometheusService := schemas.PrometheusServiceFromServiceSpec(&spec)
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
