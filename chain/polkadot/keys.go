package polkadot

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v2"

	schnorrkel "github.com/ChainSafe/go-schnorrkel/1"
	"github.com/StirlingMarketingGroup/go-namecase"
	p2pCrypto "github.com/libp2p/go-libp2p-core/crypto"
	"golang.org/x/crypto/blake2b"
)

const (
	ss58Ed25519HDKDPrefix = "Ed25519HDKD"
	ss58Secp256k1Prefix   = "Secp256k1HDKD"
)

var DEV_SEED, _ = hex.DecodeString("fac7959dbfe72f052e5a0c3c8d6530f202b02fd8f9f5ca3580ec8deb7797479e")

func DeriveEd25519FromName(name string) (*p2pCrypto.Ed25519PrivateKey, error) {
	chainCode := make([]byte, 32)
	derivePath := []byte{byte(len(name) << 2)}
	derivePath = append(derivePath, []byte(namecase.New().NameCase(name))...)
	_ = copy(chainCode, []byte(derivePath))

	hasher, err := blake2b.New256(nil)
	if err != nil {
		return nil, fmt.Errorf("error constructing hasher: %w", err)
	}

	toHash := []byte{byte(len(ss58Ed25519HDKDPrefix) << 2)}
	toHash = append(toHash, []byte(ss58Ed25519HDKDPrefix)...)
	toHash = append(toHash, DEV_SEED...)
	toHash = append(toHash, chainCode...)

	if _, err := hasher.Write(toHash); err != nil {
		return nil, fmt.Errorf("error writing data to hasher: %w", err)
	}

	newKey := hasher.Sum(nil)

	if err != nil {
		return nil, fmt.Errorf("error deriving: %w", err)
	}
	privKey := ed25519.NewKeyFromSeed(newKey)
	pubKey := privKey.Public().(ed25519.PublicKey)
	key := []byte{}
	key = append(key, privKey.Seed()...)
	key = append(key, pubKey...)

	priv, err := p2pCrypto.UnmarshalEd25519PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling: %w", err)
	}
	return priv.(*p2pCrypto.Ed25519PrivateKey), nil
}

func DeriveSr25519FromName(path []string) (*schnorrkel.MiniSecretKey, error) {
	var miniSecretSeed [32]byte
	_ = copy(miniSecretSeed[:], DEV_SEED[:32])
	miniSecret, err := schnorrkel.NewMiniSecretKeyFromRaw(miniSecretSeed)
	if err != nil {
		return nil, fmt.Errorf("error getting mini secret from seed: %w", err)
	}
	for _, pathItem := range path {
		var chainCode [32]byte
		derivePath := []byte{byte(len(pathItem) << 2)}
		derivePath = append(derivePath, []byte(pathItem)...)
		_ = copy(chainCode[:], []byte(derivePath))
		miniSecret, _, err = miniSecret.HardDeriveMiniSecretKey([]byte{}, chainCode)
		if err != nil {
			return nil, fmt.Errorf("error hard deriving mini secret key")
		}
	}
	privKey := make([]byte, 32)
	for i, mkByte := range miniSecret.ExpandEd25519().Encode() {
		privKey[i] = mkByte
	}
	pubKey := make([]byte, 32)
	for i, mkByte := range miniSecret.Public().Encode() {
		pubKey[i] = mkByte
	}

	return miniSecret, nil
}

func DeriveSecp256k1FromName(name string) (*secp256k1.PrivateKey, error) {
	chainCode := make([]byte, 32)
	derivePath := []byte{byte(len(name) << 2)}
	derivePath = append(derivePath, []byte(namecase.New().NameCase(name))...)
	_ = copy(chainCode, []byte(derivePath))

	hasher, err := blake2b.New256(nil)
	if err != nil {
		return nil, fmt.Errorf("error constructing hasher: %w", err)
	}

	toHash := []byte{byte(len(ss58Secp256k1Prefix) << 2)}
	toHash = append(toHash, []byte(ss58Secp256k1Prefix)...)
	toHash = append(toHash, DEV_SEED...)
	toHash = append(toHash, chainCode...)

	if _, err := hasher.Write(toHash); err != nil {
		return nil, fmt.Errorf("error writing data to hasher: %w", err)
	}

	newKey := hasher.Sum(nil)

	if err != nil {
		return nil, fmt.Errorf("error deriving: %w", err)
	}
	privKey, _ := secp256k1.PrivKeyFromBytes(newKey)

	return privKey, nil
}
