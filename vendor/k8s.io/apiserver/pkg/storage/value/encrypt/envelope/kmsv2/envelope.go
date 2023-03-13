/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package kmsv2 transforms values for storage at rest using a Envelope v2 provider
package kmsv2

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/gogo/protobuf/proto"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/storage/value"
	kmstypes "k8s.io/apiserver/pkg/storage/value/encrypt/envelope/kmsv2/v2alpha1"
	"k8s.io/apiserver/pkg/storage/value/encrypt/envelope/metrics"
	"k8s.io/klog/v2"
	kmsservice "k8s.io/kms/service"
	"k8s.io/utils/clock"
)

const (
	// KMSAPIVersion is the version of the KMS API.
	KMSAPIVersion = "v2alpha1"
	// annotationsMaxSize is the maximum size of the annotations.
	annotationsMaxSize = 32 * 1024 // 32 kB
	// keyIDMaxSize is the maximum size of the keyID.
	keyIDMaxSize = 1 * 1024 // 1 kB
	// encryptedDEKMaxSize is the maximum size of the encrypted DEK.
	encryptedDEKMaxSize = 1 * 1024 // 1 kB
	// cacheTTL is the default time-to-live for the cache entry.
	cacheTTL = 1 * time.Hour
)

type KeyIDGetterFunc func(context.Context) (keyID string, err error)

type envelopeTransformer struct {
	envelopeService kmsservice.Service

	keyIDGetter KeyIDGetterFunc

	// baseTransformerFunc creates a new transformer for encrypting the data with the DEK.
	baseTransformerFunc func(cipher.Block) value.Transformer
	// cache is a thread-safe expiring lru cache which caches decrypted DEKs indexed by their encrypted form.
	cache *simpleCache
}

// NewEnvelopeTransformer returns a transformer which implements a KEK-DEK based envelope encryption scheme.
// It uses envelopeService to encrypt and decrypt DEKs. Respective DEKs (in encrypted form) are prepended to
// the data items they encrypt.
func NewEnvelopeTransformer(envelopeService kmsservice.Service, keyIDGetter KeyIDGetterFunc, baseTransformerFunc func(cipher.Block) value.Transformer) value.Transformer {
	return newEnvelopeTransformerWithClock(envelopeService, keyIDGetter, baseTransformerFunc, cacheTTL, clock.RealClock{})
}

func newEnvelopeTransformerWithClock(envelopeService kmsservice.Service, keyIDGetter KeyIDGetterFunc, baseTransformerFunc func(cipher.Block) value.Transformer, cacheTTL time.Duration, clock clock.Clock) value.Transformer {
	return &envelopeTransformer{
		envelopeService:     envelopeService,
		keyIDGetter:         keyIDGetter,
		cache:               newSimpleCache(clock, cacheTTL),
		baseTransformerFunc: baseTransformerFunc,
	}
}

// TransformFromStorage decrypts data encrypted by this transformer using envelope encryption.
func (t *envelopeTransformer) TransformFromStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, bool, error) {
	metrics.RecordArrival(metrics.FromStorageLabel, time.Now())

	// Deserialize the EncryptedObject from the data.
	encryptedObject, err := t.doDecode(data)
	if err != nil {
		return nil, false, err
	}

	// Look up the decrypted DEK from cache or Envelope.
	transformer := t.cache.get(encryptedObject.EncryptedDEK)
	if transformer == nil {
		value.RecordCacheMiss()

		uid := string(uuid.NewUUID())
		klog.V(6).InfoS("Decrypting content using envelope service", "uid", uid, "key", string(dataCtx.AuthenticatedData()))
		key, err := t.envelopeService.Decrypt(ctx, uid, &kmsservice.DecryptRequest{
			Ciphertext:  encryptedObject.EncryptedDEK,
			KeyID:       encryptedObject.KeyID,
			Annotations: encryptedObject.Annotations,
		})
		if err != nil {
			return nil, false, fmt.Errorf("failed to decrypt DEK, error: %w", err)
		}

		transformer, err = t.addTransformer(encryptedObject.EncryptedDEK, key)
		if err != nil {
			return nil, false, err
		}
	}

	out, stale, err := transformer.TransformFromStorage(ctx, encryptedObject.EncryptedData, dataCtx)
	if err != nil {
		return nil, false, err
	}
	if stale {
		return out, stale, nil
	}

	// Check keyID freshness in addition to data staleness
	keyID, err := t.keyIDGetter(ctx)
	if err != nil {
		return nil, false, err
	}
	return out, encryptedObject.KeyID != keyID, nil

}

// TransformToStorage encrypts data to be written to disk using envelope encryption.
func (t *envelopeTransformer) TransformToStorage(ctx context.Context, data []byte, dataCtx value.Context) ([]byte, error) {
	metrics.RecordArrival(metrics.ToStorageLabel, time.Now())
	newKey, err := generateKey(32)
	if err != nil {
		return nil, err
	}

	uid := string(uuid.NewUUID())
	klog.V(6).InfoS("encrypting content using envelope service", "uid", uid, "key", string(dataCtx.AuthenticatedData()))
	resp, err := t.envelopeService.Encrypt(ctx, uid, newKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt DEK, error: %w", err)
	}

	transformer, err := t.addTransformer(resp.Ciphertext, newKey)
	if err != nil {
		return nil, err
	}

	result, err := transformer.TransformToStorage(ctx, data, dataCtx)
	if err != nil {
		return nil, err
	}

	encObject := &kmstypes.EncryptedObject{
		KeyID:         resp.KeyID,
		EncryptedDEK:  resp.Ciphertext,
		EncryptedData: result,
		Annotations:   resp.Annotations,
	}

	// Check keyID freshness and write to log if key IDs are different
	statusKeyID, err := t.keyIDGetter(ctx)
	if err == nil && encObject.KeyID != statusKeyID {
		klog.V(2).InfoS("observed different key IDs when encrypting content using kms v2 envelope service", "uid", uid, "objectKeyID", encObject.KeyID, "statusKeyID", statusKeyID)
	}

	// Serialize the EncryptedObject to a byte array.
	return t.doEncode(encObject)
}

// addTransformer inserts a new transformer to the Envelope cache of DEKs for future reads.
func (t *envelopeTransformer) addTransformer(encKey []byte, key []byte) (value.Transformer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	transformer := t.baseTransformerFunc(block)
	// TODO(aramase): Add metrics for cache fill percentage with custom cache implementation.
	t.cache.set(encKey, transformer)
	return transformer, nil
}

// doEncode encodes the EncryptedObject to a byte array.
func (t *envelopeTransformer) doEncode(request *kmstypes.EncryptedObject) ([]byte, error) {
	if err := validateEncryptedObject(request); err != nil {
		return nil, err
	}
	return proto.Marshal(request)
}

// doDecode decodes the byte array to an EncryptedObject.
func (t *envelopeTransformer) doDecode(originalData []byte) (*kmstypes.EncryptedObject, error) {
	o := &kmstypes.EncryptedObject{}
	if err := proto.Unmarshal(originalData, o); err != nil {
		return nil, err
	}
	// validate the EncryptedObject
	if err := validateEncryptedObject(o); err != nil {
		return nil, err
	}

	return o, nil
}

// generateKey generates a random key using system randomness.
func generateKey(length int) (key []byte, err error) {
	defer func(start time.Time) {
		value.RecordDataKeyGeneration(start, err)
	}(time.Now())
	key = make([]byte, length)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}

	return key, nil
}

func validateEncryptedObject(o *kmstypes.EncryptedObject) error {
	if o == nil {
		return fmt.Errorf("encrypted object is nil")
	}
	if len(o.EncryptedData) == 0 {
		return fmt.Errorf("encrypted data is empty")
	}
	if err := validateEncryptedDEK(o.EncryptedDEK); err != nil {
		return fmt.Errorf("failed to validate encrypted DEK: %w", err)
	}
	if err := ValidateKeyID(o.KeyID); err != nil {
		return fmt.Errorf("failed to validate key id: %w", err)
	}
	if err := validateAnnotations(o.Annotations); err != nil {
		return fmt.Errorf("failed to validate annotations: %w", err)
	}
	return nil
}

// validateEncryptedDEK tests the following:
// 1. The encrypted DEK is not empty.
// 2. The size of encrypted DEK is less than 1 kB.
func validateEncryptedDEK(encryptedDEK []byte) error {
	if len(encryptedDEK) == 0 {
		return fmt.Errorf("encrypted DEK is empty")
	}
	if len(encryptedDEK) > encryptedDEKMaxSize {
		return fmt.Errorf("encrypted DEK is %d bytes, which exceeds the max size of %d", len(encryptedDEK), encryptedDEKMaxSize)
	}
	return nil
}

// validateAnnotations tests the following:
//  1. checks if the annotation key is fully qualified
//  2. The size of annotations keys + values is less than 32 kB.
func validateAnnotations(annotations map[string][]byte) error {
	var errs []error
	var totalSize uint64
	for k, v := range annotations {
		if fieldErr := validation.IsFullyQualifiedDomainName(field.NewPath("annotations"), k); fieldErr != nil {
			errs = append(errs, fieldErr.ToAggregate())
		}
		totalSize += uint64(len(k)) + uint64(len(v))
	}
	if totalSize > annotationsMaxSize {
		errs = append(errs, fmt.Errorf("total size of annotations is %d, which exceeds the max size of %d", totalSize, annotationsMaxSize))
	}
	return utilerrors.NewAggregate(errs)
}

// ValidateKeyID tests the following:
// 1. The keyID is not empty.
// 2. The size of keyID is less than 1 kB.
func ValidateKeyID(keyID string) error {
	if len(keyID) == 0 {
		return fmt.Errorf("keyID is empty")
	}
	if len(keyID) > keyIDMaxSize {
		return fmt.Errorf("keyID is %d bytes, which exceeds the max size of %d", len(keyID), keyIDMaxSize)
	}
	return nil
}
