package registry_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"go.etcd.io/etcd/api/v3/etcdserverpb"

	"ssle/schemas"

	"ssle/registry/utils"
)

func (state RegistryAPIState) registerService(w http.ResponseWriter, r *http.Request) {
	name, dc, location, err := state.extractNameLocation(r)
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
		Node:       schemas.PathSegment(name),

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

	svcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/%v/%v/%v",
		utils.ServiceNamespace,
		spec.ServiceName,
		location,
		dc,
		name,
		spec.Instance,
	)
	dsSvcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/%v/%v",
		utils.DCServicesNamespace,
		dc,
		name,
		spec.ServiceName,
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

	txn := &etcdserverpb.TxnRequest{
		Success: []*etcdserverpb.RequestOp{
			{
				Request: &etcdserverpb.RequestOp_RequestPut{
					RequestPut: &etcdserverpb.PutRequest{
						Key:   svcKey,
						Value: serializedSpec,
					},
				},
			},
			{
				Request: &etcdserverpb.RequestOp_RequestPut{
					RequestPut: &etcdserverpb.PutRequest{
						Key:   dsSvcKey,
						Value: []byte{},
					},
				},
			},
		},
	}

	if serializedPrometheusService != nil {
		promSvcKey := fmt.Appendf(
			nil,
			"%v/%v/%v/%v/%v",
			utils.PrometheusServicesNamespace,
			dc,
			name,
			spec.ServiceName,
			spec.Instance,
		)

		txn.Success = append(txn.Success, &etcdserverpb.RequestOp{
			Request: &etcdserverpb.RequestOp_RequestPut{
				RequestPut: &etcdserverpb.PutRequest{
					Key:   promSvcKey,
					Value: serializedPrometheusService,
				},
			},
		})
	}

	res, err := state.etcdServer.Txn(r.Context(), txn)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if !res.Succeeded {
		log.Printf("Error: Failed to register service: %v", res)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	utils.HttpRespondJson(w, http.StatusOK, spec)
}
