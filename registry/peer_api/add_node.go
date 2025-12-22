package peer_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"go.etcd.io/etcd/api/v3/etcdserverpb"

	"ssle/schemas"

	"ssle/registry/utils"
)

type AddNodeResponse struct {
	Crt string `json:"crt"`
	Key string `json:"key"`
}

func (state PeerAPIState) addNodeHandler(w http.ResponseWriter, r *http.Request) {
	addNodeReq, err := utils.DeserializeRequestBody[schemas.NodeSchema](w, r)
	if err != nil {
		return
	}

	nodeKey := fmt.Appendf(nil, "%v/%v/%v", utils.NodesNamespace, addNodeReq.Datacenter, addNodeReq.Name)

	serializedNode, err := json.Marshal(addNodeReq)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	res, err := state.etcdServer.Txn(r.Context(), &etcdserverpb.TxnRequest{
		Compare: []*etcdserverpb.Compare{{
			Result: etcdserverpb.Compare_EQUAL,
			Target: etcdserverpb.Compare_CREATE,
			Key:    nodeKey,
			TargetUnion: &etcdserverpb.Compare_CreateRevision{
				CreateRevision: int64(0),
			},
		}},
		Success: []*etcdserverpb.RequestOp{{
			Request: &etcdserverpb.RequestOp_RequestPut{
				RequestPut: &etcdserverpb.PutRequest{
					Key:   nodeKey,
					Value: serializedNode,
				},
			},
		}},
	})
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if !res.Succeeded {
		http.Error(w, "", http.StatusConflict)
		return
	}

	crt, key := utils.CreateAgentCrt(
		state.state,
		addNodeReq.Datacenter.String(),
		addNodeReq.Name.String(),
	)

	utils.HttpRespondJson(w, http.StatusCreated, AddNodeResponse{
		Crt: string(crt),
		Key: string(key),
	})
}
