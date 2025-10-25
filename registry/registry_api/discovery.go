package registry_api

import (
	"fmt"
	"log"
	"net/http"

	"ssle/registry/utils"

	"go.etcd.io/etcd/server/v3/storage/mvcc"
)

func (state RegistryAPIState) prometheusDiscovery(w http.ResponseWriter, r *http.Request) {
	dc, _, err := state.extractNameLocation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	prefix := fmt.Sprintf("%v/%v", utils.DCServicesNamespace, dc)
	bytes := []byte(prefix)

	kv := state.etcdServer.KV()
	res, err := kv.Range(r.Context(), bytes, utils.PrefixEnd(bytes), mvcc.RangeOptions{})
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

	for i, kv := range res.KVs {
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
