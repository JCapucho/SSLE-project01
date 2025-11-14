package utils

import (
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"time"

	"aidanwoods.dev/go-paseto"

	"ssle/schemas"
)

const (
	ContentTypeJSON = "application/json"

	TokenHeader = "Authorization"

	DCImplicit = "DC"

	ServiceNamespace            = "svc"
	DCServicesNamespace         = "dcsvc"
	PrometheusServicesNamespace = "prom"
	NodesNamespace              = "nodes"
)

func NewToken(exp time.Duration) paseto.Token {
	token := paseto.NewToken()

	now := time.Now()
	token.SetIssuedAt(now)
	token.SetNotBefore(now)
	token.SetExpiration(now.Add(exp))

	return token
}

func ExtractTokenKey[T any](token *paseto.Token, key string) (T, error) {
	var val T
	err := token.Get(key, &val)
	if err != nil {
		return val, err
	}
	return val, nil
}

func DeserializeRequestBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var req T

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, err
	}

	err = schemas.Validate.Struct(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, err
	}

	return req, nil
}

func HttpRespondJson[T any](w http.ResponseWriter, statusCode int, val T) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(statusCode)
	err := json.NewEncoder(w).Encode(val)
	if err != nil {
		log.Printf("Error while responding with JSON body: %v", err.Error())
	}
}

func PrefixEnd(prefix []byte) []byte {
	end := make([]byte, len(prefix))
	copy(end, prefix)

	for i, v := range slices.Backward(prefix) {
		if v < 0xff {
			end[i] += 1
			break
		}
	}

	return end
}
