package ibc

import (
	"context"

	ibcexported "github.com/cosmos/ibc-go/v3/modules/core/03-connection/types"
)

type ChannelCounterparty struct {
	PortID    string `json:"port_id"`
	ChannelID string `json:"channel_id"`
}

type ChannelOutput struct {
	State          string              `json:"state"`
	Ordering       string              `json:"ordering"`
	Counterparty   ChannelCounterparty `json:"counterparty"`
	ConnectionHops []string            `json:"connection_hops"`
	Version        string              `json:"version"`
	PortID         string              `json:"port_id"`
	ChannelID      string              `json:"channel_id"`
}

type ConnectionOutput struct {
	ID           string                    `json:"id,omitempty" yaml:"id"`
	ClientID     string                    `json:"client_id,omitempty" yaml:"client_id"`
	Versions     []*ibcexported.Version    `json:"versions,omitempty" yaml:"versions"`
	State        string                    `json:"state,omitempty" yaml:"state"`
	Counterparty *ibcexported.Counterparty `json:"counterparty" yaml:"counterparty"`
	DelayPeriod  string                    `json:"delay_period,omitempty" yaml:"delay_period"`
}

type RelayerWallet struct {
	Mnemonic string `json:"mnemonic"`
	Address  string `json:"address"`
}

type Relayer interface {
	// restore a mnemonic to be used as a relayer wallet for a chain
	RestoreKey(ctx context.Context, chainID, keyName, mnemonic string) error

	// generate a new key
	AddKey(ctx context.Context, chainID, keyName string) (RelayerWallet, error)

	// add relayer configuration for a chain
	AddChainConfiguration(ctx context.Context, chainConfig ChainConfig, keyName, rpcAddr, grpcAddr string) error

	// generate new path between two chains
	GeneratePath(ctx context.Context, srcChainID, dstChainID, pathName string) error

	// setup channels, connections, and clients
	LinkPath(ctx context.Context, pathName string) error

	// update clients, such as after new genesis
	UpdateClients(ctx context.Context, pathName string) error

	// get channel IDs for chain
	GetChannels(ctx context.Context, chainID string) ([]ChannelOutput, error)

	// GetConnections returns the Connection information for a specified chain.
	GetConnections(ctx context.Context, chainID string) ([]*ConnectionOutput, error)

	// after configuration is initialized, begin relaying
	StartRelayer(ctx context.Context, pathName string) error

	// relay queue until it is empty
	ClearQueue(ctx context.Context, pathName string, channelID string) error

	// shutdown relayer
	StopRelayer(ctx context.Context) error

	// CreateClients performs the client handshake steps necessary for creating a light client
	// on src that tracks the state of dst, and a light client on dst that tracks the state of src.
	CreateClients(ctx context.Context, pathName string) error

	// CreateConnections performs the connection handshake steps necessary for creating a connection
	// between the src and dst chains.
	CreateConnections(ctx context.Context, pathName string) error
}
