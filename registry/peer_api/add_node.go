package peer_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"

	"ssle/schemas"

	"ssle/registry/utils"
)

type AddNodeRequest struct {
	Name       schemas.PathSegment `json:"name" validate:"required"`
	Datacenter schemas.PathSegment `json:"dc" validate:"required"`
	Location   schemas.PathSegment `json:"location" validate:"required"`
}

type AddNodeResponse struct {
	Token string `json:"token"`
}

func (state PeerAPIState) addNodeHandler(w http.ResponseWriter, r *http.Request) {
	addNodeReq, err := utils.DeserializeRequestBody[AddNodeRequest](w, r)
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

	txn := &etcdserverpb.TxnRequest{
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
	}

	res, err := state.etcdServer.Txn(r.Context(), txn)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if !res.Succeeded {
		http.Error(w, "", http.StatusConflict)
		return
	}

	token := utils.NewToken(365 * 24 * time.Hour)
	token.SetString("name", addNodeReq.Name.String())
	token.SetString("dc", addNodeReq.Datacenter.String())
	token.SetString("loc", addNodeReq.Location.String())

	encryptedToken := token.V4Encrypt(state.state.TokenKey, []byte(utils.DCImplicit))

	utils.HttpRespondJson(w, http.StatusCreated, AddNodeResponse{
		Token: encryptedToken,
	})
}
