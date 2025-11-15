package registry_api

import (
	"fmt"
	"log"
	"net/http"

	"ssle/registry/utils"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

func (state RegistryAPIState) prometheusDiscovery(w http.ResponseWriter, r *http.Request) {
	_, dc, _, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	prefix := fmt.Appendf(nil, "%v/%v", utils.PrometheusServicesNamespace, dc)

	res, err := state.etcdServer.Range(r.Context(), &etcdserverpb.RangeRequest{
		Key:      prefix,
		RangeEnd: utils.PrefixEnd(prefix),
	})
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", utils.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_, err = w.Write([]byte("["))
	if err != nil {
		return
	}

	for i, kv := range res.Kvs {
		if i != 0 {
			_, err = w.Write([]byte(","))
			if err != nil {
				return
			}
		}

		_, err = w.Write(kv.Value)
		if err != nil {
			return
		}
	}

	_, err = w.Write([]byte("]"))
	if err != nil {
		return
	}
}
