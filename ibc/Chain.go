package ibc

import (
	"context"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/ory/dockertest"
)

type ChainConfig struct {
	Type           string
	Name           string
	ChainID        string
	Repository     string
	Version        string
	Bin            string
	Bech32Prefix   string
	Denom          string
	GasPrices      string
	GasAdjustment  float64
	TrustingPeriod string
}

type WalletAmount struct {
	Address string
	Denom   string
	Amount  int64
}

type IBCTimeout struct {
	NanoSeconds uint64
	Height      uint64
}

type Chain interface {
	// fetch chain configuration
	Config() ChainConfig

	// initializes node structs so that things like initializing keys can be done before starting the chain
	Initialize(testName string, homeDirectory string, dockerPool *dockertest.Pool, networkID string) error

	// sets up everything needed (validators, gentx, fullnodes, peering, additional accounts) for chain to start from genesis
	Start(testName string, ctx context.Context, additionalGenesisWallets []WalletAmount) error

	// start a chain with a provided genesis file. Will override validators for first 2/3 of voting power
	StartWithGenesisFile(testName string, ctx context.Context, home string, pool *dockertest.Pool, networkID string, genesisFilePath string) error

	// export state at specific height
	ExportState(ctx context.Context, height int64) (string, error)

	// retrieves rpc address that can be reached by other containers in the docker network
	GetRPCAddress() string

	// retrieves grpc address that can be reached by other containers in the docker network
	GetGRPCAddress() string

	// get current height
	Height() (int64, error)

	// creates a test key in the "user" node, (either the first fullnode or the first validator if no fullnodes)
	CreateKey(ctx context.Context, keyName string) error

	// fetches the bech32 address for a test key on the "user" node (either the first fullnode or the first validator if no fullnodes)
	GetAddress(keyName string) ([]byte, error)

	// send funds to wallet from user account
	SendFunds(ctx context.Context, keyName string, amount WalletAmount) error

	// sends an IBC transfer from a test key on the "user" node (either the first fullnode or the first validator if no fullnodes)
	// returns tx hash
	SendIBCTransfer(ctx context.Context, channelID, keyName string, amount WalletAmount, timeout *IBCTimeout) (string, error)

	// takes file path to smart contract and initialization message. returns contract address
	InstantiateContract(ctx context.Context, keyName string, amount WalletAmount, fileName, initMessage string, needsNoAdminFlag bool) (string, error)

	// executes a contract transaction with a message using it's address
	ExecuteContract(ctx context.Context, keyName string, contractAddress string, message string) error

	// dump state of contract at block height
	DumpContractState(ctx context.Context, contractAddress string, height int64) (*DumpContractStateResponse, error)

	// create balancer pool
	CreatePool(ctx context.Context, keyName string, contractAddress string, swapFee float64, exitFee float64, assets []WalletAmount) error

	// waits for # of blocks to be produced. Returns latest height
	WaitForBlocks(number int64) (int64, error)

	// fetch balance for a specific account address and denom
	GetBalance(ctx context.Context, address string, denom string) (int64, error)

	// get the fees in native denom for an amount of spent gas
	GetGasFeesInNativeDenom(gasPaid int64) int64

	// fetch transaction
	GetTransaction(ctx context.Context, txHash string) (*types.TxResponse, error)

	// RegisterInterchainAccount will register an interchain account on behalf of the calling chain (controller chain)
	// on the counterparty chain (the host chain).
	RegisterInterchainAccount(ctx context.Context, keyName, connectionID string) (string, error)

	// SendICABankTransfer will send a bank transfer msg from the fromAddr to the toAddr for the given amount and denom.
	SendICABankTransfer(ctx context.Context, connectionID, fromAddr, toAddr, denom string, amount int64) error

	// QueryInterchainAccount will query the interchain account that was created on behalf of the specified address.
	QueryInterchainAccount(ctx context.Context, connectionID, address string) (string, error)
}
