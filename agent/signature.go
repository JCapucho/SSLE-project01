// Adapted from:
// https://github.com/sigstore/sigstore-go/blob/cc06490446765e67a0e63797659f1439c3f53cc0/examples/oci-image-verification/main.go
//
// Original copyright notice is reproduced below:
//
// Copyright 2023 The Sigstore Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"ssle/agent/state"
)

var (
	LocalImageErr = errors.New("local image cannot be verified")
)

func VerifyImageSignature(image *image.InspectResponse, state *state.State) (*verify.VerificationResult, error) {
	bundle, artifactDigest, err := bundleFromImage(image, true, false)
	if err != nil {
		return nil, err
	}

	artifactDigestBytes, err := hex.DecodeString(*artifactDigest)
	if err != nil {
		return nil, err
	}

	artifactPolicy := verify.WithArtifactDigest("sha256", artifactDigestBytes)

	res, err := state.SignatureVerifier.Verify(
		bundle,
		verify.NewPolicy(artifactPolicy, verify.WithCertificateIdentity(*state.SignatureIdentity)),
	)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// bundleFromImage returns a Bundle based on the image
func bundleFromImage(image *image.InspectResponse, hasTlog, hasTimestamp bool) (*bundle.Bundle, *string, error) {
	if len(image.RepoDigests) < 1 {
		return nil, nil, LocalImageErr
	}

	digest, err := name.NewDigest(image.RepoDigests[0])
	if err != nil {
		return nil, nil, err
	}

	// First try to obtain the bundle from the referrers
	referrers, err := remote.Referrers(digest)
	if err != nil {
		return nil, nil, err
	}

	referrerManifest, err := referrers.IndexManifest()
	if err != nil {
		return nil, nil, err
	}

	for _, v := range referrerManifest.Manifests {
		mfDigest := digest.Context().Digest(v.Digest.String())
		mf, err := remote.Get(mfDigest)
		if err != nil {
			return nil, nil, fmt.Errorf("error downloading signature manifest: %w", err)
		}

		sigManifest, err := v1.ParseManifest(bytes.NewReader(mf.Manifest))
		if err != nil {
			return nil, nil, fmt.Errorf("error parsing signature manifest: %w", err)
		}

		if len(sigManifest.Layers) < 1 {
			return nil, nil, errors.New("signature manifest contains no layers")
		}

		if !strings.HasPrefix(string(sigManifest.Layers[0].MediaType), "application/vnd.dev.sigstore.bundle") {
			continue
		}

		layerDigest := digest.Context().Digest(sigManifest.Layers[0].Digest.String())
		layer, err := remote.Layer(layerDigest)
		if err != nil {
			return nil, nil, fmt.Errorf("error downloading signature layer: %w", err)
		}

		layerReader, err := layer.Uncompressed()
		if err != nil {
			return nil, nil, fmt.Errorf("error opening layer contents reader: %w", err)
		}

		var bun bundle.Bundle
		err = json.NewDecoder(layerReader).Decode(&bun)
		if err != nil {
			return nil, nil, fmt.Errorf("error decoding bundle: %w", err)
		}

		layerReader.Close()

		// 5. Return the bundle and the digest of the simple signing layer (this is what is signed)
		h, err := v1.NewHash(digest.Identifier())
		return &bun, &h.Hex, nil
	}

	// No bundle was found in the referrers so try to find it the old way

	// 1. Get the simple signing layer
	simpleSigning, err := simpleSigningLayerFromImage(digest)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting simple signing layer: %w", err)
	}

	// 2. Build the verification material for the bundle
	verificationMaterial, err := getBundleVerificationMaterial(simpleSigning, hasTlog, hasTimestamp)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting verification material: %w", err)
	}

	// 3. Build the message signature for the bundle
	msgSignature, err := getBundleMsgSignature(simpleSigning)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting message signature: %w", err)
	}

	// 4. Construct and verify the bundle
	bundleMediaType, err := bundle.MediaTypeString("0.1")
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bundle media type: %w", err)
	}
	pb := protobundle.Bundle{
		MediaType:            bundleMediaType,
		VerificationMaterial: verificationMaterial,
		Content:              msgSignature,
	}
	bun, err := bundle.NewBundle(&pb)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating bundle: %w", err)
	}

	// 5. Return the bundle and the digest of the simple signing layer (this is what is signed)
	return bun, &simpleSigning.Digest.Hex, nil
}

// simpleSigningLayerFromImage returns the simple signing layer from the image
func simpleSigningLayerFromImage(digest name.Digest) (*v1.Descriptor, error) {
	// 1. Construct the signature layer reference from the repo digest of the image
	h, err := v1.NewHash(digest.Identifier())
	sigTag := digest.Context().Tag(fmt.Sprint(h.Algorithm, "-", h.Hex, ".sig"))

	// 2. Get the manifest of the signature
	mf, err := crane.Manifest(sigTag.Name())
	if err != nil {
		return nil, fmt.Errorf("error getting signature manifest: %w", err)
	}
	sigManifest, err := v1.ParseManifest(bytes.NewReader(mf))
	if err != nil {
		return nil, fmt.Errorf("error parsing signature manifest: %w", err)
	}

	// 3. Ensure there is at least one layer and it is a simple signing layer
	if len(sigManifest.Layers) == 0 || sigManifest.Layers[0].MediaType != "application/vnd.dev.cosign.simplesigning.v1+json" {
		return nil, fmt.Errorf("no suitable layers found in signature manifest")
	}

	// 4. Return the layer - most probably there are more layers (one for each signature) but verifying one is enough
	return &sigManifest.Layers[0], nil
}

// getBundleVerificationMaterial returns the bundle verification material from the simple signing layer
func getBundleVerificationMaterial(manifestLayer *v1.Descriptor, hasTlog, hasTimestamp bool) (*protobundle.VerificationMaterial, error) {
	// 1. Get the signing certificate chain
	signingCert, err := getVerificationMaterialX509CertificateChain(manifestLayer)
	if err != nil {
		return nil, fmt.Errorf("error getting signing certificate: %w", err)
	}

	// 2. Get the transparency log entries
	var tlogEntries []*protorekor.TransparencyLogEntry
	if hasTlog {
		tlogEntries, err = getVerificationMaterialTlogEntries(manifestLayer)
		if err != nil {
			return nil, fmt.Errorf("error getting tlog entries: %w", err)
		}
	}
	var timestampEntries *protobundle.TimestampVerificationData
	if hasTimestamp {
		timestampEntries, err = getVerificationMaterialTimestampEntries(manifestLayer)
		if err != nil {
			return nil, fmt.Errorf("error getting timestamp entries: %w", err)
		}
	}

	// 3. Construct the verification material
	return &protobundle.VerificationMaterial{
		Content:                   signingCert,
		TlogEntries:               tlogEntries,
		TimestampVerificationData: timestampEntries,
	}, nil
}

// getVerificationMaterialTlogEntries returns the verification material transparency log entries from the simple signing layer
func getVerificationMaterialTlogEntries(manifestLayer *v1.Descriptor) ([]*protorekor.TransparencyLogEntry, error) {
	// 1. Get the bundle annotation
	bun := manifestLayer.Annotations["dev.sigstore.cosign/bundle"]
	var jsonData map[string]interface{}
	err := json.Unmarshal([]byte(bun), &jsonData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling json: %w", err)
	}

	// 2. Get the log index, log ID, integrated time, signed entry timestamp and body
	logIndex, ok := jsonData["Payload"].(map[string]interface{})["logIndex"].(float64)
	if !ok {
		return nil, fmt.Errorf("error getting logIndex")
	}
	li, ok := jsonData["Payload"].(map[string]interface{})["logID"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting logID")
	}
	logID, err := hex.DecodeString(li)
	if err != nil {
		return nil, fmt.Errorf("error decoding logID: %w", err)
	}
	integratedTime, ok := jsonData["Payload"].(map[string]interface{})["integratedTime"].(float64)
	if !ok {
		return nil, fmt.Errorf("error getting integratedTime")
	}
	set, ok := jsonData["SignedEntryTimestamp"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting SignedEntryTimestamp")
	}
	signedEntryTimestamp, err := base64.StdEncoding.DecodeString(set)
	if err != nil {
		return nil, fmt.Errorf("error decoding signedEntryTimestamp: %w", err)
	}

	// 3. Unmarshal the body and extract the rekor KindVersion details
	body, ok := jsonData["Payload"].(map[string]interface{})["body"].(string)
	if !ok {
		return nil, fmt.Errorf("error getting body")
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("error decoding body: %w", err)
	}
	err = json.Unmarshal(bodyBytes, &jsonData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling json: %w", err)
	}
	apiVersion := jsonData["apiVersion"].(string)
	kind := jsonData["kind"].(string)

	// 4. Construct the transparency log entry list
	return []*protorekor.TransparencyLogEntry{
		{
			LogIndex: int64(logIndex),
			LogId: &protocommon.LogId{
				KeyId: logID,
			},
			KindVersion: &protorekor.KindVersion{
				Kind:    kind,
				Version: apiVersion,
			},
			IntegratedTime: int64(integratedTime),
			InclusionPromise: &protorekor.InclusionPromise{
				SignedEntryTimestamp: signedEntryTimestamp,
			},
			InclusionProof:    nil,
			CanonicalizedBody: bodyBytes,
		},
	}, nil
}

func getVerificationMaterialTimestampEntries(manifestLayer *v1.Descriptor) (*protobundle.TimestampVerificationData, error) {
	// 1. Get the bundle annotation
	ts := manifestLayer.Annotations["dev.sigstore.cosign/rfc3161timestamp"]

	// 2. Get the key/value pairs maps
	var keyValPairs map[string]string
	err := json.Unmarshal([]byte(ts), &keyValPairs)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON blob into key/val map: %w", err)
	}

	// 3. Verify the key "SignedRFC3161Timestamp" is present
	if _, ok := keyValPairs["SignedRFC3161Timestamp"]; !ok {
		return nil, errors.New("error getting SignedRFC3161Timestamp from key/value pairs")
	}

	// 4. Decode the base64 encoded timestamp
	der, err := base64.StdEncoding.DecodeString(keyValPairs["SignedRFC3161Timestamp"])
	if err != nil {
		return nil, fmt.Errorf("error decoding base64 encoded timestamp: %w", err)
	}

	// 5. Construct the timestamp entry list
	return &protobundle.TimestampVerificationData{
		Rfc3161Timestamps: []*protocommon.RFC3161SignedTimestamp{
			{
				SignedTimestamp: der,
			},
		},
	}, nil
}

// getVerificationMaterialX509CertificateChain returns the verification material X509 certificate chain from the simple signing layer
func getVerificationMaterialX509CertificateChain(manifestLayer *v1.Descriptor) (*protobundle.VerificationMaterial_X509CertificateChain, error) {
	// 1. Get the PEM certificate from the simple signing layer
	pemCert := manifestLayer.Annotations["dev.sigstore.cosign/certificate"]

	// 2. Construct the DER encoded version of the PEM certificate
	block, _ := pem.Decode([]byte(pemCert))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}
	signingCert := protocommon.X509Certificate{
		RawBytes: block.Bytes,
	}

	// 3. Construct the X509 certificate chain
	return &protobundle.VerificationMaterial_X509CertificateChain{
		X509CertificateChain: &protocommon.X509CertificateChain{
			Certificates: []*protocommon.X509Certificate{&signingCert},
		},
	}, nil
}

// getBundleMsgSignature returns the bundle message signature from the simple signing layer
func getBundleMsgSignature(simpleSigningLayer *v1.Descriptor) (*protobundle.Bundle_MessageSignature, error) {
	// 1. Get the message digest algorithm
	var msgHashAlg protocommon.HashAlgorithm
	switch simpleSigningLayer.Digest.Algorithm {
	case "sha256":
		msgHashAlg = protocommon.HashAlgorithm_SHA2_256
	default:
		return nil, fmt.Errorf("unknown digest algorithm: %s", simpleSigningLayer.Digest.Algorithm)
	}

	// 2. Get the message digest
	digest, err := hex.DecodeString(simpleSigningLayer.Digest.Hex)
	if err != nil {
		return nil, fmt.Errorf("error decoding digest: %w", err)
	}

	// 3. Get the signature
	s := simpleSigningLayer.Annotations["dev.cosignproject.cosign/signature"]
	sig, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("error decoding manSig: %w", err)
	}

	// Construct the bundle message signature
	return &protobundle.Bundle_MessageSignature{
		MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{
				Algorithm: msgHashAlg,
				Digest:    digest,
			},
			Signature: sig,
		},
	}, nil
}
