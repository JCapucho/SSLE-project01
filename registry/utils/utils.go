package utils

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.etcd.io/etcd/server/v3/lease"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"ssle/registry/schemas"
	"ssle/registry/state"
)

const (
	ServiceNamespace            = "svc"
	DCServicesNamespace         = "dcsvc"
	PrometheusServicesNamespace = "prom"
	NodesNamespace              = "nodes"
	NodesLeasesNamespace        = "node_lease"
	PeerAgentApiNamespace       = "peer_agent_api"

	AgentCertificateOU            = "Agents"
	AgentCertificateExpiry        = 7 * 24 * time.Hour
	NodeKeepaliveTTL       uint32 = 30
)

var (
	AuthFailure = status.Errorf(codes.Unauthenticated, "Authentication failure")
	ServerError = status.Errorf(codes.Internal, "Internal server error")
)

func ExtractPeerCertificate(ctx context.Context) (*x509.Certificate, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, AuthFailure
	}

	mtls, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, AuthFailure
	}

	if len(mtls.State.PeerCertificates) < 1 {
		return nil, AuthFailure
	}

	return mtls.State.PeerCertificates[0], nil
}

func ExtractPeerDatacenterNode(cert *x509.Certificate) (string, string, error) {
	if len(cert.Subject.OrganizationalUnit) != 2 {
		return "", "", errors.New("Malformed agent certificate")
	}

	if cert.Subject.OrganizationalUnit[1] != AgentCertificateOU {
		return "", "", errors.New("Malformed agent certificate")
	}

	return cert.Subject.OrganizationalUnit[0], cert.Subject.CommonName, nil
}

func GetNodeSchema(ctx context.Context, etcd *etcdserver.EtcdServer, dc string, nodeName string) (*schemas.NodeSchema, error) {
	nodeKey := fmt.Appendf(nil, "%v/%v/%v", NodesNamespace, dc, nodeName)

	res, err := etcd.Range(ctx, &etcdserverpb.RangeRequest{
		Key:   nodeKey,
		Limit: int64(1),
	})
	if err != nil {
		return nil, err
	}

	if len(res.Kvs) < 1 {
		return nil, nil
	}

	var node schemas.NodeSchema
	err = json.Unmarshal(res.Kvs[0].Value, &node)
	if err != nil {
		return nil, err
	}

	return &node, nil
}

func AuthenticateAgentFromCertificate(ctx context.Context, cert *x509.Certificate, etcd *etcdserver.EtcdServer) (*schemas.NodeSchema, error) {
	dc, name, err := ExtractPeerDatacenterNode(cert)
	if err != nil {
		log.Printf("Error getting node auth: %v", err)
		return nil, AuthFailure
	}

	node, err := GetNodeSchema(ctx, etcd, dc, name)
	if err != nil {
		log.Printf("Error getting node schema: %v", err)
		return nil, AuthFailure
	}

	if node == nil {
		log.Printf("Error getting node schema: %v/%v does not exist", dc, name)
		return nil, AuthFailure
	}

	return node, nil
}

func AuthenticateAgent(ctx context.Context, etcd *etcdserver.EtcdServer) (*schemas.NodeSchema, error) {
	cert, err := ExtractPeerCertificate(ctx)
	if err != nil {
		return nil, err
	}
	return AuthenticateAgentFromCertificate(ctx, cert, etcd)
}

func GetNodeLease(ctx context.Context, etcd *etcdserver.EtcdServer, dc string, nodeName string) (int64, error) {
	key := fmt.Appendf(nil, "%v/%v/%v", NodesLeasesNamespace, dc, nodeName)

	for range 5 {
		// Get lease number
		res, err := etcd.Range(ctx, &etcdserverpb.RangeRequest{Key: key})
		if err != nil {
			return 0, ServerError
		}

		if len(res.Kvs) != 0 {
			var leaseId int64
			err := json.Unmarshal(res.Kvs[0].Value, &leaseId)
			// If we have an error in decoding the lease id, something must have
			// gone wrong when storing it, so ignore it and proceed as if no
			// lease id was allocated.
			if err == nil {
				_, err := etcd.LeaseRenew(ctx, lease.LeaseID(leaseId))
				// If we could renew the lease, stop processing, otherwise the
				// lease might have expired so a new one needs to be created.
				if err == nil {
					return leaseId, nil
				}
			}
		}

		lease, err := etcd.LeaseGrant(ctx, &etcdserverpb.LeaseGrantRequest{
			TTL: int64(NodeKeepaliveTTL),
		})
		if err != nil {
			return 0, ServerError
		}

		serialized, err := json.Marshal(lease.ID)
		if err != nil {
			return 0, ServerError
		}

		txn := &etcdserverpb.TxnRequest{
			Success: []*etcdserverpb.RequestOp{
				{
					Request: &etcdserverpb.RequestOp_RequestPut{
						RequestPut: &etcdserverpb.PutRequest{
							Key:   key,
							Value: serialized,
						},
					},
				},
			},
		}

		if len(res.Kvs) != 0 {
			// Key exists, check that it wasn't modified since we last saw it
			txn.Compare = append(txn.Compare, &etcdserverpb.Compare{
				Result: etcdserverpb.Compare_EQUAL,
				Target: etcdserverpb.Compare_VERSION,
				Key:    key,
				TargetUnion: &etcdserverpb.Compare_Version{
					Version: res.Kvs[0].Version,
				},
			})
		} else {
			// Key does not exist, ensure a new one wasn't created since we last saw it
			txn.Compare = append(txn.Compare, &etcdserverpb.Compare{
				Result: etcdserverpb.Compare_EQUAL,
				Target: etcdserverpb.Compare_CREATE,
				Key:    key,
				TargetUnion: &etcdserverpb.Compare_CreateRevision{
					CreateRevision: int64(0),
				},
			})
		}

		txnRes, err := etcd.Txn(ctx, txn)
		if err != nil {
			return 0, ServerError
		}

		if txnRes.Succeeded {
			return lease.ID, nil
		}

		log.Print("Transaction failed to update lease for node, retrying")
	}

	return 0, ServerError
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

func CreateAgentCrt(state *state.State, datacenter string, node string) ([]byte, []byte) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err.Error())
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(AgentCertificateExpiry)

	template := x509.Certificate{
		Subject: pkix.Name{
			Organization:       []string{"SSLE Project 01"},
			OrganizationalUnit: []string{AgentCertificateOU, datacenter},
			CommonName:         node,
		},
		IPAddresses:           []net.IP{},
		DNSNames:              []string{},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, state.AgentCA.Leaf, pub, state.AgentCA.PrivateKey)
	if err != nil {
		panic(err.Error())
	}

	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		panic(err.Error())
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDer})

	return crt, key
}
