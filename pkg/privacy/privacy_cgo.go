//go:build cgo && (linux || darwin)

package privacy

/*
#cgo CXXFLAGS: -I${SRCDIR}/../../cpp/include
#cgo LDFLAGS: -L${SRCDIR}/../../cpp/build -lprivacy -lsodium -lstdc++
#include "privacy/encryption.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

const (
	PublicKeyBytes = 32
	SecretKeyBytes = 32
	SealBytes      = 48
)

func Init() error {
	if int(C.init_privacy_layer()) != 0 {
		return errors.New("privacy init failed (libsodium)")
	}
	return nil
}

func GenerateSequencerKeys() (pub []byte, sec []byte, err error) {
	pub = make([]byte, PublicKeyBytes)
	sec = make([]byte, SecretKeyBytes)

	ret := int(C.generate_sequencer_keys(
		(*C.uint8_t)(unsafe.Pointer(&pub[0])),
		(*C.uint8_t)(unsafe.Pointer(&sec[0])),
	))
	if ret != 0 {
		return nil, nil, fmt.Errorf("generate_sequencer_keys failed: code=%d", ret)
	}
	return pub, sec, nil
}

func SaveKeyToFile(path string, key []byte) error {
	if len(key) == 0 {
		return errors.New("key cannot be empty")
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	ok := int(C.save_key_to_file(
		cpath,
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		C.size_t(len(key)),
	))
	if ok != 1 {
		return fmt.Errorf("failed to save key: %s", path)
	}
	return nil
}

func LoadKeyFromFile(path string, expectedLen int) ([]byte, error) {
	if expectedLen <= 0 {
		return nil, errors.New("expected key length must be > 0")
	}
	buf := make([]byte, expectedLen)
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	ok := int(C.load_key_from_file(
		cpath,
		(*C.uint8_t)(unsafe.Pointer(&buf[0])),
		C.size_t(expectedLen),
	))
	if ok != 1 {
		return nil, fmt.Errorf("failed to load key: %s", path)
	}
	return buf, nil
}

func SealTransaction(plaintext []byte, sequencerPubKey []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("plaintext cannot be empty")
	}
	if len(sequencerPubKey) != PublicKeyBytes {
		return nil, fmt.Errorf("sequencer public key must be %d bytes", PublicKeyBytes)
	}

	ciphertext := make([]byte, len(plaintext)+SealBytes)

	ret := int(C.seal_transaction(
		(*C.uint8_t)(unsafe.Pointer(&plaintext[0])),
		C.size_t(len(plaintext)),
		(*C.uint8_t)(unsafe.Pointer(&sequencerPubKey[0])),
		(*C.uint8_t)(unsafe.Pointer(&ciphertext[0])),
	))
	if ret != 0 {
		return nil, fmt.Errorf("seal_transaction failed: code=%d", ret)
	}
	return ciphertext, nil
}

func DecryptSingle(ciphertext []byte, pubKey []byte, secKey []byte) ([]byte, error) {
	if len(ciphertext) < SealBytes {
		return nil, errors.New("ciphertext too short")
	}
	if len(pubKey) != PublicKeyBytes {
		return nil, fmt.Errorf("public key must be %d bytes", PublicKeyBytes)
	}
	if len(secKey) != SecretKeyBytes {
		return nil, fmt.Errorf("secret key must be %d bytes", SecretKeyBytes)
	}

	plaintext := make([]byte, len(ciphertext)-SealBytes)

	enc := C.EncryptedTx{
		ciphertext: (*C.uint8_t)(unsafe.Pointer(&ciphertext[0])),
		length:     C.size_t(len(ciphertext)),
	}
	dec := C.DecryptedTx{
		plaintext: (*C.uint8_t)(unsafe.Pointer(&plaintext[0])),
		length:    0,
		status:    0,
	}

	ret := int(C.decrypt_single_tx(
		&enc,
		(*C.uint8_t)(unsafe.Pointer(&pubKey[0])),
		(*C.uint8_t)(unsafe.Pointer(&secKey[0])),
		&dec,
	))
	if ret != 0 || int(dec.status) != 0 {
		return nil, fmt.Errorf("decrypt_single_tx failed: ret=%d status=%d", ret, int(dec.status))
	}
	return plaintext[:int(dec.length)], nil
}

func DecryptBatch(ciphertexts [][]byte, pubKey []byte, secKey []byte) ([][]byte, error) {
	if len(pubKey) != PublicKeyBytes {
		return nil, fmt.Errorf("public key must be %d bytes", PublicKeyBytes)
	}
	if len(secKey) != SecretKeyBytes {
		return nil, fmt.Errorf("secret key must be %d bytes", SecretKeyBytes)
	}
	n := len(ciphertexts)
	if n == 0 {
		return [][]byte{}, nil
	}

	enc := make([]C.EncryptedTx, n)
	dec := make([]C.DecryptedTx, n)
	plainBuffers := make([][]byte, n)

	for i, ct := range ciphertexts {
		if len(ct) < SealBytes {
			return nil, fmt.Errorf("ciphertext[%d] too short", i)
		}
		ptLen := len(ct) - SealBytes
		if ptLen == 0 {
			ptLen = 1
		}
		plainBuffers[i] = make([]byte, ptLen)

		enc[i] = C.EncryptedTx{
			ciphertext: (*C.uint8_t)(unsafe.Pointer(&ct[0])),
			length:     C.size_t(len(ct)),
		}
		dec[i] = C.DecryptedTx{
			plaintext: (*C.uint8_t)(unsafe.Pointer(&plainBuffers[i][0])),
			length:    0,
			status:    0,
		}
	}

	ret := int(C.decrypt_batch_tx(
		(*C.EncryptedTx)(unsafe.Pointer(&enc[0])),
		C.size_t(n),
		(*C.uint8_t)(unsafe.Pointer(&pubKey[0])),
		(*C.uint8_t)(unsafe.Pointer(&secKey[0])),
		(*C.DecryptedTx)(unsafe.Pointer(&dec[0])),
	))

	out := make([][]byte, n)
	errIdx := make([]string, 0)
	for i := range dec {
		if int(dec[i].status) != 0 {
			errIdx = append(errIdx, fmt.Sprintf("%d", i))
			continue
		}
		out[i] = plainBuffers[i][:int(dec[i].length)]
	}

	if ret != 0 || len(errIdx) > 0 {
		return nil, fmt.Errorf("decrypt_batch_tx failed for indexes: %s", strings.Join(errIdx, ","))
	}

	return out, nil
}
