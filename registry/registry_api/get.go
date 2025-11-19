package registry_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"

	"go.etcd.io/etcd/api/v3/etcdserverpb"

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
	res, err := state.etcdServer.Range(ctx, &etcdserverpb.RangeRequest{
		Key:      prefix,
		RangeEnd: utils.PrefixEnd(prefix),
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, err
	}

	svcs := make(map[string]schemas.ServiceSpec, len(res.Kvs))
	for _, kv := range res.Kvs {
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

	locParam := r.PathValue("location")
	dcParam := r.PathValue("datacenter")
	nodeParam := r.PathValue("node")
	instanceParam := r.PathValue("instance")

	if locParam != "" {
		location = locParam
	}
	if dcParam != "" {
		dc = dcParam
	}
	if nodeParam != "" {
		name = nodeParam
	}

	svcPrefix := fmt.Appendf(nil, "%v/%v/", utils.ServiceNamespace, svc)
	locPrefix := fmt.Appendf(svcPrefix, "%v/", location)
	dcPrefix := fmt.Appendf(locPrefix, "%v/", dc)
	namePrefix := fmt.Appendf(dcPrefix, "%v/", name)

	var svcs map[string]schemas.ServiceSpec

	if instanceParam != "" {
		key := fmt.Appendf(namePrefix, "%v", instanceParam)
		svcs, err = state.getServiceInternal(ctx, key, 1)
	} else {
		svcs, err = state.getServiceInternal(ctx, namePrefix, MaxGetServiceLimit)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && nodeParam == "" {
		log.Print("Querying datacenter services")
		svcs, err = state.fillServices(ctx, dcPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && dcParam == "" {
		log.Print("Querying location services")
		svcs, err = state.fillServices(ctx, locPrefix, svcs)
	}

	if err == nil && len(svcs) < MaxGetServiceLimit && locParam == "" {
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
