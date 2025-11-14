package registry_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"

	"go.etcd.io/etcd/server/v3/storage/mvcc"

	"ssle/schemas"

	"ssle/registry/utils"
)

const (
	MaxGetServiceLimit = 3
)

func (state RegistryAPIState) getServiceInternal(
	ctx context.Context,
	prefix []byte,
	limit int,
) (map[string]schemas.ServiceSpec, error) {
	kv := state.etcdServer.KV()

	res, err := kv.Range(ctx, prefix, utils.PrefixEnd(prefix), mvcc.RangeOptions{
		Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}

	svcs := make(map[string]schemas.ServiceSpec, len(res.KVs))
	for _, kv := range res.KVs {
		var tmp schemas.ServiceSpec
		err = json.Unmarshal(kv.Value, &tmp)
		if err != nil {
			return nil, err
		}
		svcs[string(kv.Key)] = tmp
	}

	return svcs, nil
}

func (state RegistryAPIState) fillServices(
	ctx context.Context,
	prefix []byte,
	svcs map[string]schemas.ServiceSpec,
) (map[string]schemas.ServiceSpec, error) {
	extra, err := state.getServiceInternal(ctx, prefix, MaxGetServiceLimit)
	if err != nil {
		return svcs, nil
	}

	for k, v := range extra {
		if len(svcs) >= MaxGetServiceLimit {
			break
		}
		svcs[k] = v
	}

	return svcs, nil
}

func (state RegistryAPIState) getService(w http.ResponseWriter, r *http.Request) {
	name, dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	svc := r.PathValue("service")

	svcPrefix := fmt.Appendf(nil, "%v/%v/", utils.ServiceNamespace, svc)
	locPrefix := fmt.Appendf(svcPrefix, "%v/", location)
	dcPrefix := fmt.Appendf(locPrefix, "%v/", dc)
	namePrefix := fmt.Appendf(dcPrefix, "%v/", name)

	svcs, err := state.getServiceInternal(ctx, namePrefix, MaxGetServiceLimit)

	if err == nil && len(svcs) < MaxGetServiceLimit {
		log.Print("Querying datacenter services")
		svcs, err = state.fillServices(ctx, dcPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit {
		log.Print("Querying location services")
		svcs, err = state.fillServices(ctx, locPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit {
		log.Print("Querying global services")
		svcs, err = state.fillServices(ctx, svcPrefix, svcs)
	}

	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	utils.HttpRespondJson(w, http.StatusOK, slices.Collect(maps.Values(svcs)))
}
