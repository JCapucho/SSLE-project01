package peer_api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.etcd.io/etcd/pkg/v3/traceutil"
	"go.etcd.io/etcd/server/v3/lease"
	"go.etcd.io/etcd/server/v3/storage/mvcc"

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
	kv := state.etcdServer.KV()

	tx := kv.Write(traceutil.New("Register service", state.etcdServer.Logger()))

	res, err := tx.Range(r.Context(), nodeKey, nil, mvcc.RangeOptions{})
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	if len(res.KVs) != 0 {
		http.Error(w, "", http.StatusConflict)
		tx.End()
		return
	}

	serializedNode, err := json.Marshal(addNodeReq)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "", http.StatusInternalServerError)
		tx.End()
		return
	}

	tx.Put(nodeKey, serializedNode, lease.NoLease)
	tx.End()
	kv.Commit()

	token := utils.NewToken(365 * 24 * time.Hour)
	token.SetString("name", addNodeReq.Name.String())
	token.SetString("dc", addNodeReq.Datacenter.String())
	token.SetString("loc", addNodeReq.Location.String())

	encryptedToken := token.V4Encrypt(state.state.TokenKey, []byte(utils.DCImplicit))

	utils.HttpRespondJson(w, http.StatusCreated, AddNodeResponse{
		Token: encryptedToken,
	})
}
