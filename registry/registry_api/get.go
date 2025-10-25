package registry_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"

	"ssle/registry/schema"
	"ssle/registry/utils"

	"go.etcd.io/etcd/server/v3/storage/mvcc"
)

const (
	MaxGetServiceLimit = 3
)

func (state RegistryAPIState) getServiceInternal(
	ctx context.Context,
	prefix string,
	limit int,
) (map[string]schema.ServiceSpec, error) {
	kv := state.etcdServer.KV()

	bytes := []byte(prefix)
	res, err := kv.Range(ctx, bytes, utils.PrefixEnd(bytes), mvcc.RangeOptions{
		Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}

	svcs := make(map[string]schema.ServiceSpec, len(res.KVs))
	for _, kv := range res.KVs {
		var tmp schema.ServiceSpec
		err = json.Unmarshal(kv.Value, &tmp)
		if err != nil {
			return nil, err
		}
		svcs[string(kv.Key)] = tmp
	}

	return svcs, nil
}

func (state RegistryAPIState) getService(w http.ResponseWriter, r *http.Request) {
	dc, location, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	svc := r.PathValue("service")

	svcPrefix := fmt.Sprintf("%v/%v/", utils.ServiceNamespace, svc)
	locPrefix := fmt.Sprintf("%v%v/", svcPrefix, location)
	dcPrefix := fmt.Sprintf("%v%v/", locPrefix, dc)

	var extra map[string]schema.ServiceSpec
	svcs, err := state.getServiceInternal(ctx, dcPrefix, MaxGetServiceLimit)

	if err == nil && len(svcs) < MaxGetServiceLimit {
		log.Print("Querying location services")
		extra, err = state.getServiceInternal(ctx, locPrefix, MaxGetServiceLimit)
		if err == nil {
			for k, v := range extra {
				if len(svcs) >= MaxGetServiceLimit {
					break
				}
				svcs[k] = v
			}
		}
	}

	if err == nil && len(svcs) < MaxGetServiceLimit {
		log.Print("Querying global services")
		extra, err = state.getServiceInternal(ctx, svcPrefix, MaxGetServiceLimit)
		if err == nil {
			for k, v := range extra {
				if len(svcs) >= MaxGetServiceLimit {
					break
				}
				svcs[k] = v
			}
		}
	}

	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	utils.HttpRespondJson(w, http.StatusOK, slices.Collect(maps.Values(svcs)))
}
