// Package rly provides an interface to the cosmos relayer running in a Docker container.
package rly

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/ory/dockertest/v3"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/relayer"
	"go.uber.org/zap"
)

// CosmosRelayer is the ibc.Relayer implementation for github.com/cosmos/relayer.
type CosmosRelayer struct {
	// Embedded DockerRelayer so commands just work.
	*relayer.DockerRelayer
}

func NewCosmosRelayer(log *zap.Logger, testName, home string, pool *dockertest.Pool, networkID string) *CosmosRelayer {
	c := commander{log: log}
	r := &CosmosRelayer{
		DockerRelayer: relayer.NewDockerRelayer(log, testName, home, pool, networkID, c),
	}

	if err := os.MkdirAll(r.Dir(), 0755); err != nil {
		panic(fmt.Errorf("failed to initialize directory for relayer: %w", err))
	}

	return r
}

type CosmosRelayerChainConfigValue struct {
	AccountPrefix  string  `json:"account-prefix"`
	ChainID        string  `json:"chain-id"`
	Debug          bool    `json:"debug"`
	GRPCAddr       string  `json:"grpc-addr"`
	GasAdjustment  float64 `json:"gas-adjustment"`
	GasPrices      string  `json:"gas-prices"`
	Key            string  `json:"key"`
	KeyringBackend string  `json:"keyring-backend"`
	OutputFormat   string  `json:"output-format"`
	RPCAddr        string  `json:"rpc-addr"`
	SignMode       string  `json:"sign-mode"`
	Timeout        string  `json:"timeout"`
}

type CosmosRelayerChainConfig struct {
	Type  string                        `json:"type"`
	Value CosmosRelayerChainConfigValue `json:"value"`
}

const (
	ContainerImage   = "ghcr.io/cosmos/relayer"
	ContainerVersion = "v2.0.0-beta4"
)

// Capabilities returns the set of capabilities of the Cosmos relayer.
//
// Note, this API may change if the rly package eventually needs
// to distinguish between multiple rly versions.
func Capabilities() map[relayer.Capability]bool {
	m := relayer.FullCapabilities()
	m[relayer.TimestampTimeout] = false
	return m
}

func ChainConfigToCosmosRelayerChainConfig(chainConfig ibc.ChainConfig, keyName, rpcAddr, gprcAddr, websocketAddr string) CosmosRelayerChainConfig {
	return CosmosRelayerChainConfig{
		Type: chainConfig.Type,
		Value: CosmosRelayerChainConfigValue{
			Key:            keyName,
			ChainID:        chainConfig.ChainID,
			RPCAddr:        rpcAddr,
			GRPCAddr:       gprcAddr,
			AccountPrefix:  chainConfig.Bech32Prefix,
			KeyringBackend: keyring.BackendTest,
			GasAdjustment:  chainConfig.GasAdjustment,
			GasPrices:      chainConfig.GasPrices,
			Debug:          true,
			Timeout:        "10s",
			OutputFormat:   "json",
			SignMode:       "direct",
		},
	}
}

// commander satisfies relayer.RelayerCommander.
type commander struct {
	log *zap.Logger
}

func (commander) Name() string {
	return "rly"
}

func (commander) AddChainConfiguration(containerFilePath, homeDir string) []string {
	return []string{
		"rly", "chains", "add", "-f", containerFilePath,
		"--home", homeDir,
	}
}

func (commander) AddKey(chainID, keyName, homeDir string) []string {
	return []string{
		"rly", "keys", "add", chainID, keyName,
		"--home", homeDir,
	}
}

func (commander) ClearQueue(pathName, channelID, homeDir string) []string {
	return []string{
		"rly", "tx", "relay-pkts", pathName, channelID,
		"--home", homeDir,
	}
}

func (commander) CreateChannel(pathName string, opts ibc.CreateChannelOptions, homeDir string) []string {
	return []string{
		"rly", "tx", "channel", pathName,
		"--src-port", opts.SourcePortName,
		"--dst-port", opts.DestPortName,
		"--order", opts.Order,
		"--version", opts.Version,

		"--home", homeDir,
	}
}

func (commander) CreateClients(pathName, homeDir string) []string {
	return []string{
		"rly", "tx", "clients", pathName,
		"--home", homeDir,
	}
}

func (commander) CreateConnections(pathName, homeDir string) []string {
	return []string{
		"rly", "tx", "connection", pathName,
		"--home", homeDir,
	}
}

func (commander) GeneratePath(srcChainID, dstChainID, pathName, homeDir string) []string {
	return []string{
		"rly", "paths", "new", srcChainID, dstChainID, pathName,
		"--home", homeDir,
	}
}

func (commander) GetChannels(chainID, homeDir string) []string {
	return []string{
		"rly", "q", "channels", chainID,
		"--home", homeDir,
	}
}

func (commander) GetConnections(chainID, homeDir string) []string {
	return []string{
		"rly", "q", "connections", chainID,
		"--home", homeDir,
	}
}

func (commander) LinkPath(pathName, homeDir string) []string {
	return []string{
		"rly", "tx", "link", pathName,
		"--home", homeDir,
	}
}

func (commander) RestoreKey(chainID, keyName, mnemonic, homeDir string) []string {
	return []string{
		"rly", "keys", "restore", chainID, keyName, mnemonic,
		"--home", homeDir,
	}
}

func (commander) StartRelayer(pathName, homeDir string) []string {
	return []string{
		"rly", "start", pathName, "--debug",
		"--home", homeDir,
	}
}

func (commander) UpdateClients(pathName, homeDir string) []string {
	return []string{
		"rly", "tx", "update-clients", pathName,
		"--home", homeDir,
	}
}

func (commander) ConfigContent(ctx context.Context, cfg ibc.ChainConfig, keyName, rpcAddr, grpcAddr, websocketAddr string) ([]byte, error) {
	cosmosRelayerChainConfig := ChainConfigToCosmosRelayerChainConfig(cfg, keyName, rpcAddr, grpcAddr, websocketAddr)
	jsonBytes, err := json.Marshal(cosmosRelayerChainConfig)
	if err != nil {
		return nil, err
	}
	return jsonBytes, nil
}

func (commander) ContainerImage() string {
	return ContainerImage
}

func (commander) ContainerVersion() string {
	return ContainerVersion
}

func (commander) ParseAddKeyOutput(stdout, stderr string) (ibc.RelayerWallet, error) {
	var wallet ibc.RelayerWallet
	err := json.Unmarshal([]byte(stdout), &wallet)
	return wallet, err
}

func (c commander) ParseGetChannelsOutput(stdout, stderr string) ([]ibc.ChannelOutput, error) {
	var channels []ibc.ChannelOutput
	channelSplit := strings.Split(stdout, "\n")
	for _, channel := range channelSplit {
		if strings.TrimSpace(channel) == "" {
			continue
		}
		var channelOutput ibc.ChannelOutput
		err := json.Unmarshal([]byte(channel), &channelOutput)
		if err != nil {
			c.log.Error("Failed to parse channels json", zap.Error(err))
			continue
		}
		channels = append(channels, channelOutput)
	}

	return channels, nil
}

func (c commander) ParseGetConnectionsOutput(stdout, stderr string) (ibc.ConnectionOutputs, error) {
	var connections ibc.ConnectionOutputs
	for _, connection := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(connection) == "" {
			continue
		}

		var connectionOutput ibc.ConnectionOutput
		if err := json.Unmarshal([]byte(connection), &connectionOutput); err != nil {
			c.log.Error(
				"Error parsing connection json",
				zap.Error(err),
			)

			continue
		}
		connections = append(connections, &connectionOutput)
	}

	return connections, nil
}

func (commander) Init(homeDir string) []string {
	return []string{
		"rly", "config", "init",
		"--home", homeDir,
	}
}
