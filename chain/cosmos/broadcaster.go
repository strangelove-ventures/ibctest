package cosmos

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/strangelove-ventures/ibctest/broadcast"
	"github.com/strangelove-ventures/ibctest/internal/dockerutil"
)

var _ broadcast.Broadcaster = &Broadcaster{}

type Broadcaster struct {
	// buf stores the output sdk.TxResponse when broadcast.Tx is invoked.
	buf *bytes.Buffer
	// keyrings is a mapping of keyrings which point to a temporary test directory. The contents
	// of this directory are copied from the node container for the specific user.
	keyrings map[broadcast.User]keyring.Keyring

	// chain is a reference to the CosmosChain instance which will be the target of the messages.
	chain *CosmosChain
	// t is the testing.T for the current test.
	t *testing.T

	// factoryOptions is a slice of broadcast.FactoryOpt which enables arbitrary configuration of the tx.Factory.
	factoryOptions []broadcast.FactoryOpt
	// clientContextOptions is a slice of broadcast.ClientContextOpt which enables arbitrary configuration of the client.Context.
	clientContextOptions []broadcast.ClientContextOpt
}

// NewBroadcaster returns a instance of Broadcaster which can be used with broadcast.Tx to
// broadcast messages sdk messages.
func NewBroadcaster(t *testing.T, chain *CosmosChain) *Broadcaster {
	return &Broadcaster{
		t:        t,
		chain:    chain,
		buf:      &bytes.Buffer{},
		keyrings: map[broadcast.User]keyring.Keyring{},
	}
}

// ConfigureFactoryOptions ensure the given configuration functions are run when calling GetFactory
// after all default options have been applied.
func (b *Broadcaster) ConfigureFactoryOptions(opts ...broadcast.FactoryOpt) {
	b.factoryOptions = append(b.factoryOptions, opts...)
}

// ConfigureClientContextOptions ensure the given configuration functions are run when calling GetClientContext
// after all default options have been applied.
func (b *Broadcaster) ConfigureClientContextOptions(opts ...broadcast.ClientContextOpt) {
	b.clientContextOptions = append(b.clientContextOptions, opts...)
}

// GetFactory returns an instance of tx.Factory that is configured with this Broadcaster's CosmosChain
// and the provided user. ConfigureFactoryOptions can be used to specify arbitrary options to configure the returned
// factory.
func (b *Broadcaster) GetFactory(ctx context.Context, user broadcast.User) (tx.Factory, error) {
	clientContext, err := b.GetClientContext(ctx, user)
	if err != nil {
		return tx.Factory{}, err
	}

	sdkAdd, err := sdk.AccAddressFromBech32(user.Bech32Address(b.chain.Config().Bech32Prefix))
	if err != nil {
		return tx.Factory{}, err
	}

	accNumber, err := clientContext.AccountRetriever.GetAccount(clientContext, sdkAdd)
	if err != nil {
		return tx.Factory{}, err
	}

	f := b.defaultTxFactory(clientContext, accNumber.GetAccountNumber())
	for _, opt := range b.factoryOptions {
		f = opt(f)
	}
	return f, nil
}

// GetClientContext returns a client context that is configured with this Broadcaster's CosmosChain and
// the provided user. ConfigureClientContextOptions can be used to configure arbitrary options to configure the returned
// client.Context.
func (b *Broadcaster) GetClientContext(ctx context.Context, user broadcast.User) (client.Context, error) {
	chain := b.chain
	cn := chain.getFullNode()

	_, ok := b.keyrings[user]
	if !ok {
		localDir := b.t.TempDir()
		containerKeyringDir := filepath.Join(cn.HomeDir(), "keyring-test")
		kr, err := dockerutil.NewLocalKeyringFromDockerContainer(ctx, cn.DockerClient, localDir, containerKeyringDir, cn.containerID, ctx)
		if err != nil {
			return client.Context{}, err
		}
		b.keyrings[user] = kr
	}

	sdkAdd, err := sdk.AccAddressFromBech32(user.Bech32Address(chain.Config().Bech32Prefix))
	if err != nil {
		return client.Context{}, err
	}

	clientContext := b.defaultClientContext(user, sdkAdd)
	for _, opt := range b.clientContextOptions {
		clientContext = opt(clientContext)
	}
	return clientContext, nil
}

// GetTxResponseBytes returns the sdk.TxResponse bytes which returned from broadcast.Tx.
func (b *Broadcaster) GetTxResponseBytes(ctx context.Context, user broadcast.User) ([]byte, error) {
	if b.buf == nil || b.buf.Len() == 0 {
		return nil, fmt.Errorf("empty buffer, transaction has not been executed yet")
	}
	return b.buf.Bytes(), nil
}

// UnmarshalTxResponseBytes accepts the sdk.TxResponse bytes and unmarshalls them into an
// instance of sdk.TxResponse.
func (b *Broadcaster) UnmarshalTxResponseBytes(ctx context.Context, bytes []byte) (sdk.TxResponse, error) {
	resp := sdk.TxResponse{}
	if err := defaultEncoding.Codec.UnmarshalJSON(bytes, &resp); err != nil {
		return sdk.TxResponse{}, err
	}
	return resp, nil
}

// defaultClientContext returns a default client context configured with the user as the sender.
func (b *Broadcaster) defaultClientContext(fromUser broadcast.User, sdkAdd sdk.AccAddress) client.Context {
	// initialize a clean buffer each time
	b.buf.Reset()
	kr := b.keyrings[fromUser]
	cn := b.chain.getFullNode()
	return cn.CliContext().
		WithOutput(b.buf).
		WithFrom(fromUser.Bech32Address(b.chain.Config().Bech32Prefix)).
		WithFromAddress(sdkAdd).
		WithFromName(fromUser.GetKeyName()).
		WithSkipConfirmation(true).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithKeyring(kr).
		WithBroadcastMode(flags.BroadcastBlock).
		WithCodec(defaultEncoding.Codec).
		WithHomeDir(cn.Home)

}

// defaultTxFactory creates a new Factory with default configuration.
func (b *Broadcaster) defaultTxFactory(clientCtx client.Context, accountNumber uint64) tx.Factory {
	chainConfig := b.chain.Config()
	return tx.Factory{}.
		WithAccountNumber(accountNumber).
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT).
		WithGasAdjustment(chainConfig.GasAdjustment).
		WithGas(flags.DefaultGasLimit).
		WithGasPrices(chainConfig.GasPrices).
		WithMemo("ibctest").
		WithTxConfig(clientCtx.TxConfig).
		WithAccountRetriever(clientCtx.AccountRetriever).
		WithKeybase(clientCtx.Keyring).
		WithChainID(clientCtx.ChainID).
		WithSimulateAndExecute(false)
}
