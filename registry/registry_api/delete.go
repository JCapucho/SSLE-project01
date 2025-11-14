package registry_api

import (
	"bytes"
	"fmt"
	"net/http"

	"ssle/registry/utils"

	"go.etcd.io/etcd/pkg/v3/traceutil"
	"go.etcd.io/etcd/server/v3/storage/mvcc"
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

	kv := state.etcdServer.KV()
	tx := kv.Write(traceutil.New("Clean node", state.etcdServer.Logger()))

	res, err := tx.Range(ctx, nodeSvcKey, utils.PrefixEnd(nodeSvcKey), mvcc.RangeOptions{})
	for _, kvRes := range res.KVs {
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
		tx.DeleteRange(svcKey, utils.PrefixEnd(svcKey))
	}
	tx.DeleteRange(nodeSvcKey, utils.PrefixEnd(nodeSvcKey))
	tx.DeleteRange(promSvcKey, utils.PrefixEnd(promSvcKey))

	tx.End()
	kv.Commit()

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

	kv := state.etcdServer.KV()
	tx := kv.Write(traceutil.New("Remove service instance", state.etcdServer.Logger()))

	tx.DeleteRange(svcKey, nil)
	tx.DeleteRange(dsSvcKey, nil)
	tx.DeleteRange(promSvcKey, nil)

	tx.End()
	kv.Commit()

	w.WriteHeader(http.StatusNoContent)
}
