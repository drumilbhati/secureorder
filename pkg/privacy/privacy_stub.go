//go:build !cgo || (!linux && !darwin)

package privacy

import "errors"

const (
	PublicKeyBytes = 32
	SecretKeyBytes = 32
	SealBytes      = 48
)

func Init() error {
	return errors.New("privacy layer requires cgo with linux/darwin toolchain and built cpp library")
}

func GenerateSequencerKeys() ([]byte, []byte, error) {
	return nil, nil, errors.New("privacy layer unavailable in this build")
}

func SaveKeyToFile(_ string, _ []byte) error {
	return errors.New("privacy layer unavailable in this build")
}

func LoadKeyFromFile(_ string, _ int) ([]byte, error) {
	return nil, errors.New("privacy layer unavailable in this build")
}

func SealTransaction(_ []byte, _ []byte) ([]byte, error) {
	return nil, errors.New("privacy layer unavailable in this build")
}

func DecryptSingle(_ []byte, _ []byte, _ []byte) ([]byte, error) {
	return nil, errors.New("privacy layer unavailable in this build")
}

func DecryptBatch(_ [][]byte, _ []byte, _ []byte) ([][]byte, error) {
	return nil, errors.New("privacy layer unavailable in this build")
}
