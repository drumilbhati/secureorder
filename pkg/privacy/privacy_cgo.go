//go:build cgo && (linux || darwin)

// Package privacy is the Go-side wrapper around the C++ libsodium encryption layer.
//
// All cryptographic operations are implemented in C++ (cpp/src/privacy/) and
// exposed through a C header (encryption.h). This file uses CGO to call those
// functions from Go code.
//
// The encryption scheme is NaCl crypto_box_seal (curve25519-xsalsa20-poly1305):
//   - curve25519  — key agreement (derives a shared secret from an ephemeral keypair)
//   - xsalsa20    — stream cipher (encrypts the payload)
//   - poly1305    — MAC (authenticates the ciphertext)
//
// Sealed-box overhead: 48 bytes (32 ephemeral pubkey + 16 MAC tag) per message.
// The sender is anonymous — no sender keypair is required for SealTransaction.
package privacy

/*
#cgo CPPFLAGS: -I${SRCDIR}/../../build_wrapper/include
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -L${SRCDIR}/../../cpp/build/lib -lprivacy -lsodium -lstdc++
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

// Key and overhead size constants. These mirror the libsodium constants
// crypto_box_PUBLICKEYBYTES, crypto_box_SECRETKEYBYTES, and crypto_box_SEALBYTES.
// Centralising them here avoids magic numbers throughout the Go codebase.
const (
	PublicKeyBytes = 32 // curve25519 public key — 256-bit
	SecretKeyBytes = 32 // curve25519 secret key — 256-bit
	SealBytes      = 48 // 32-byte ephemeral pubkey + 16-byte Poly1305 MAC
)

// Init initialises libsodium. Must be called once at startup before any
// encrypt/decrypt operation. Internally calls sodium_init(), which seeds
// the platform CSPRNG.
//
// Returns an error only if libsodium fails to initialise (e.g. no entropy
// source available), which should not happen on any modern OS.
func Init() error {
	if int(C.init_privacy_layer()) != 0 {
		return errors.New("privacy init failed (libsodium)")
	}
	return nil
}

// GenerateSequencerKeys generates a fresh Curve25519 keypair for the sequencer.
//
// The public key is distributed to clients so they can seal transactions.
// The secret key must be kept confidential — it is the only key that can
// decrypt sealed transactions.
//
// Keys are 32 bytes each (256-bit). Use SaveKeyToFile to persist them.
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

// SaveKeyToFile persists a raw key to a binary file on disk.
// The C++ implementation opens the file and writes exactly len(key) bytes.
// Returns an error if the file cannot be created or written.
func SaveKeyToFile(path string, key []byte) error {
	if len(key) == 0 {
		return errors.New("key cannot be empty")
	}
	// C.CString allocates a null-terminated copy in C memory.
	// defer C.free releases it when this function returns.
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

// LoadKeyFromFile reads exactly expectedLen bytes from a binary key file.
// Returns an error if the file does not exist, cannot be opened, or contains
// fewer bytes than expectedLen.
//
// Use privacy.PublicKeyBytes or privacy.SecretKeyBytes for expectedLen.
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

// SealTransaction encrypts plaintext using the sequencer's public key.
//
// The sender is anonymous: the function generates an ephemeral Curve25519 keypair
// internally and discards the private half after deriving the shared secret.
// Only the holder of sequencerPubKey's corresponding secret key can decrypt.
//
// Output length is always len(plaintext) + SealBytes (48 bytes).
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

// DecryptSingle decrypts one sealed ciphertext using the sequencer's keypair.
//
// Both pubKey and secKey are required because crypto_box_seal_open uses the
// public key to re-derive the ephemeral shared secret and the secret key to
// perform the actual decryption.
//
// The ciphertext must be longer than SealBytes (48 bytes) — if it is shorter
// there is not enough data to even parse the ephemeral pubkey and MAC.
//
// CGO pointer rules: Go slices cannot be passed to C if they contain Go
// pointers. We use C.CBytes to copy each slice into C-managed memory first.
func DecryptSingle(ciphertext []byte, pubKey []byte, secKey []byte) ([]byte, error) {
	if len(ciphertext) <= SealBytes {
		return nil, fmt.Errorf("ciphertext too short")
	}
	if len(pubKey) != PublicKeyBytes {
		return nil, fmt.Errorf("public key must be %d bytes", PublicKeyBytes)
	}
	if len(secKey) != SecretKeyBytes {
		return nil, fmt.Errorf("secret key must be %d bytes", SecretKeyBytes)
	}

	// Copy all inputs to C-allocated memory to satisfy CGO pointer rules:
	// C functions must not receive pointers into Go-managed heap memory.
	cCiphertext := C.CBytes(ciphertext)
	defer C.free(cCiphertext)

	cPubKey := C.CBytes(pubKey)
	defer C.free(cPubKey)

	cSecKey := C.CBytes(secKey)
	defer C.free(cSecKey)

	plaintextLen := len(ciphertext) - SealBytes
	cPlaintext := C.malloc(C.size_t(plaintextLen))
	defer C.free(cPlaintext)

	// Populate the C structs that the encryption.h API expects.
	enc := C.EncryptedTx{
		ciphertext: (*C.uint8_t)(cCiphertext),
		length:     C.size_t(len(ciphertext)),
	}
	dec := C.DecryptedTx{
		plaintext: (*C.uint8_t)(cPlaintext),
		length:    0,
		status:    0,
	}

	ret := int(C.decrypt_single_tx(
		&enc,
		(*C.uint8_t)(cPubKey),
		(*C.uint8_t)(cSecKey),
		&dec,
	))

	// Both the function return code and the per-item status must be zero.
	// A non-zero status means the MAC verification failed (wrong key or tampered ciphertext).
	if ret != 0 || int(dec.status) != 0 {
		return nil, fmt.Errorf("decrypt_single_tx failed: ret=%d status=%d", ret, int(dec.status))
	}

	// Copy the plaintext from C memory back into a Go-managed slice.
	return C.GoBytes(cPlaintext, C.int(dec.length)), nil
}

// DecryptBatch decrypts a slice of ciphertexts in parallel using the C++
// batch decryption implementation (which spawns std::thread workers).
//
// Each ciphertext is decrypted independently. A failure on one item does NOT
// abort the rest of the batch — its corresponding entry in the output slice
// will be nil and its index will appear in the returned error message.
//
// All output slices are freshly allocated Go slices copied from C memory.
// Input slices are not modified.
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

	// Allocate contiguous C arrays for the EncryptedTx and DecryptedTx structs.
	// Using C.malloc keeps the structs in C memory so CGO pointer rules are satisfied.
	encMem := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.EncryptedTx{})))
	if encMem == nil {
		return nil, errors.New("failed to allocate encrypted batch buffer")
	}
	defer C.free(encMem)

	decMem := C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.DecryptedTx{})))
	if decMem == nil {
		return nil, errors.New("failed to allocate decrypted batch buffer")
	}
	defer C.free(decMem)

	// unsafe.Slice gives us Go-slice views over the C arrays so we can index them.
	enc := unsafe.Slice((*C.EncryptedTx)(encMem), n)
	dec := unsafe.Slice((*C.DecryptedTx)(decMem), n)

	// Per-item C buffers tracked separately for cleanup.
	cipherBuffers := make([]unsafe.Pointer, n)
	plainBuffers := make([]unsafe.Pointer, n)

	for i, ct := range ciphertexts {
		if len(ct) < SealBytes {
			return nil, fmt.Errorf("ciphertext[%d] too short", i)
		}
		ptLen := len(ct) - SealBytes
		// Minimum plaintext buffer of 1 byte prevents C.malloc(0) which is
		// implementation-defined (may return nil on some platforms).
		if ptLen == 0 {
			ptLen = 1
		}

		// Copy ciphertext into C memory.
		cipherBuffers[i] = C.CBytes(ct)
		if cipherBuffers[i] == nil {
			return nil, fmt.Errorf("failed to allocate ciphertext buffer for index %d", i)
		}

		// Pre-allocate output plaintext buffer in C memory.
		plainBuffers[i] = C.malloc(C.size_t(ptLen))
		if plainBuffers[i] == nil {
			return nil, fmt.Errorf("failed to allocate plaintext buffer for index %d", i)
		}

		// Initialise the input/output structs for this batch item.
		enc[i] = C.EncryptedTx{
			ciphertext: (*C.uint8_t)(cipherBuffers[i]),
			length:     C.size_t(len(ct)),
		}
		dec[i] = C.DecryptedTx{
			plaintext: (*C.uint8_t)(plainBuffers[i]),
			length:    0,
			status:    0,
		}
	}

	// Deferred cleanup: free all per-item C buffers regardless of outcome.
	defer func() {
		for _, buf := range cipherBuffers {
			if buf != nil {
				C.free(buf)
			}
		}
		for _, buf := range plainBuffers {
			if buf != nil {
				C.free(buf)
			}
		}
	}()

	// Copy keys to C memory (CGO pointer rules).
	cPubKey := C.CBytes(pubKey)
	defer C.free(cPubKey)
	cSecKey := C.CBytes(secKey)
	defer C.free(cSecKey)

	// Call the parallel C++ batch decryptor. It spawns worker threads
	// (capped at hardware_concurrency) and distributes the batch across them.
	ret := int(C.decrypt_batch_tx(
		(*C.EncryptedTx)(encMem),
		C.size_t(n),
		(*C.uint8_t)(cPubKey),
		(*C.uint8_t)(cSecKey),
		(*C.DecryptedTx)(decMem),
	))

	// Build the output slice. For each item, copy the plaintext from C memory
	// into a new Go slice. Record which indices failed.
	out := make([][]byte, n)
	errIdx := make([]string, 0)
	for i := range dec {
		if int(dec[i].status) != 0 {
			errIdx = append(errIdx, fmt.Sprintf("%d", i))
			continue
		}
		out[i] = C.GoBytes(unsafe.Pointer(dec[i].plaintext), C.int(dec[i].length))
	}

	if ret != 0 || len(errIdx) > 0 {
		return nil, fmt.Errorf("decrypt_batch_tx failed for indexes: %s", strings.Join(errIdx, ","))
	}

	return out, nil
}
