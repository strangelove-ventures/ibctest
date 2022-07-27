package polkadot

import (
	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"
)

const (
	ss58Format = 42
	ss58Prefix = "SS58PRE"
)

func EncodeAddressSS58(key []byte) (string, error) {
	input := []byte{ss58Format}
	input = append(input, key...)

	checksum, err := ss58Checksum(input)
	if err != nil {
		return "", err
	}

	final := input
	if len(key) == 32 || len(key) == 33 {
		final = append(final, checksum[0:2]...)
	} else {
		final = append(final, checksum[0:1]...)
	}

	return base58.Encode(final), nil
}

func ss58Checksum(data []byte) ([]byte, error) {
	hasher, err := blake2b.New512(nil)
	if err != nil {
		return nil, err
	}

	if _, err := hasher.Write([]byte(ss58Prefix)); err != nil {
		return nil, err
	}

	if _, err := hasher.Write(data); err != nil {
		return nil, err
	}

	return hasher.Sum(nil), nil
}
