package registry_api

import (
	"bytes"
	"fmt"
	"log"
	"net/http"

	"ssle/registry/utils"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

func (state RegistryAPIState) deleteNodeServices(w http.ResponseWriter, r *http.Request) {
	name, dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	nodeSvcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/",
		utils.DCServicesNamespace,
		dc,
		name,
	)
	promSvcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/",
		utils.PrometheusServicesNamespace,
		dc,
		name,
	)

	ctx := r.Context()

	res, err := state.etcdServer.Range(ctx, &etcdserverpb.RangeRequest{
		Key:      nodeSvcKey,
		RangeEnd: utils.PrefixEnd(nodeSvcKey),
	})
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	txn := &etcdserverpb.TxnRequest{
		Success: []*etcdserverpb.RequestOp{},
	}

	for _, kvRes := range res.Kvs {
		parts := bytes.Split(kvRes.Key, []byte("/"))
		svcName := parts[len(parts)-2]
		svcKey := fmt.Appendf(
			nil,
			"%v/%v/%v/%v/%v",
			utils.ServiceNamespace,
			string(svcName),
			location,
			dc,
			name,
		)

		txn.Success = append(txn.Success, &etcdserverpb.RequestOp{
			Request: &etcdserverpb.RequestOp_RequestDeleteRange{
				RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
					Key:      svcKey,
					RangeEnd: utils.PrefixEnd(svcKey),
				},
			},
		})
	}

	txn.Success = append(txn.Success, &etcdserverpb.RequestOp{
		Request: &etcdserverpb.RequestOp_RequestDeleteRange{
			RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
				Key:      nodeSvcKey,
				RangeEnd: utils.PrefixEnd(nodeSvcKey),
			},
		},
	})
	txn.Success = append(txn.Success, &etcdserverpb.RequestOp{
		Request: &etcdserverpb.RequestOp_RequestDeleteRange{
			RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
				Key:      promSvcKey,
				RangeEnd: utils.PrefixEnd(promSvcKey),
			},
		},
	})

	txnRes, err := state.etcdServer.Txn(ctx, txn)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if !txnRes.Succeeded {
		log.Printf("Error: Failed to clean node: %v", res)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (state RegistryAPIState) deleteServiceInstance(w http.ResponseWriter, r *http.Request) {
	name, dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	svc := r.PathValue("service")
	instance := r.PathValue("instance")

	svcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/%v/%v/%v",
		utils.ServiceNamespace,
		svc,
		location,
		dc,
		name,
		instance,
	)
	dsSvcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/%v/%v",
		utils.DCServicesNamespace,
		dc,
		name,
		svc,
		instance,
	)
	promSvcKey := fmt.Appendf(
		nil,
		"%v/%v/%v/%v/%v",
		utils.PrometheusServicesNamespace,
		dc,
		name,
		svc,
		instance,
	)

	txn := &etcdserverpb.TxnRequest{
		Success: []*etcdserverpb.RequestOp{
			{
				Request: &etcdserverpb.RequestOp_RequestDeleteRange{
					RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
						Key: svcKey,
					},
				},
			},
			{
				Request: &etcdserverpb.RequestOp_RequestDeleteRange{
					RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
						Key: dsSvcKey,
					},
				},
			},
			{
				Request: &etcdserverpb.RequestOp_RequestDeleteRange{
					RequestDeleteRange: &etcdserverpb.DeleteRangeRequest{
						Key: promSvcKey,
					},
				},
			},
		},
	}

	res, err := state.etcdServer.Txn(r.Context(), txn)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if !res.Succeeded {
		log.Printf("Error: Failed to delete service: %v", res)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
