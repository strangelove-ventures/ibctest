package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/ory/dockertest/v3"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/relayer"
	"go.uber.org/zap"
)

type HermesPath struct {
	SrcChainID  string
	DestChainID string
}

// HermesRelayer is the ibc.Relayer implementation for github.com/informalsystems/hermes.
type HermesRelayer struct {
	// Embedded DockerRelayer so commands just work.
	*relayer.DockerRelayer
}

func NewHermesRelayer(log *zap.Logger, testName, home string, pool *dockertest.Pool, networkID string) *HermesRelayer {
	c := commander{log: log}
	r := &HermesRelayer{
		DockerRelayer: relayer.NewDockerRelayer(log, testName, home, pool, networkID, c),
	}

	if err := os.MkdirAll(r.Dir(), 0755); err != nil {
		panic(fmt.Errorf("failed to initialize directory for relayer: %w", err))
	}

	return r
}

const (
	ContainerImage   = "docker.io/informalsystems/hermes"
	ContainerVersion = "0.15.0"
)

// Capabilities returns the set of capabilities of the Hermes relayer.
//
// Note, this API may change if the hermes package eventually needs
// to distinguish between multiple hermes versions.
func Capabilities() map[relayer.Capability]bool {
	m := relayer.FullCapabilities()
	m[relayer.TimestampTimeout] = false
	return m
}

// GetGasPriceFromString attempts to parse the gas price from the string
// Hermes expects only a single gas price
func GetGasPriceFromString(gasPrices string) HermesGasPriceConfig {
	coins, err := types.ParseCoinsNormalized(gasPrices)
	if err != nil {
		panic(err)
	}

	return HermesGasPriceConfig{
		Price: coins[0].Amount.ToDec().MustFloat64(),
		Denom: coins[0].GetDenom(),
	}
}

func ChainConfigToHermesChainConfig(chainConfig ibc.ChainConfig, keyName, rpcAddr, gprcAddr, websocketAddr string) HermesChainConfig {
	return HermesChainConfig{
		ID:             chainConfig.ChainID,
		RpcAddr:        rpcAddr,
		WebSocketAddr:  websocketAddr,
		GRPCAddr:       gprcAddr,
		RPCTimeout:     "10s",
		AccountPrefix:  chainConfig.Bech32Prefix,
		KeyName:        keyName,
		KeyStoreType:   keyring.BackendTest,
		StorePrefix:    "ibc",
		DefaultGas:     100000,
		MaxGas:         400000,
		GasAdjustment:  chainConfig.GasAdjustment,
		MaxMsgNum:      30,
		MaxTxSize:      2097152,
		ClockDrift:     "5s",
		MaxBlockTime:   "30s",
		TrustingPeriod: "14days",
		MemoPrefix:     "",
		TrustThreshold: HermesTrustThresholdConfig{
			Numerator:   "1",
			Denominator: "3",
		},
		GasPrice: GetGasPriceFromString(chainConfig.GasPrices),
		AddressType: HermesAddressTypeConfig{
			Derivation: "cosmos",
		},
	}
}

// commander satisfies relayer.RelayerCommander.
type commander struct {
	log *zap.Logger
}

func (commander) Name() string {
	return "hermes"
}

//FIXME - AddChainConfiguration
func (commander) AddChainConfiguration(containerFilePath, homeDir string) []string {
	return []string{
		"hermes", "chains", "add", "-f", containerFilePath,
		"--home", homeDir,
	}
}

func (commander) AddKey(chainID, keyName, homeDir string) []string {
	return []string{
		"hermes", "keys", "add", chainID, "-n", keyName,
		"-c", filepath.Join(homeDir, "hermes", "config.toml"),
		"-j",
	}
}

// FIXME CreateConnections
func (c commander) ClearQueue(pathName, channelID, homeDir string) []string {
	chainID, err := c.getSourceChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up source chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	portID, err := c.getSourcePortIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up portID for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	return []string{
		"hermes", "clear", "packets", chainID, portID, channelID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME CreateConnections
func (c commander) CreateClients(pathName, homeDir string) []string {
	srcChainID, err := c.getSourceChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up source chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	dstChainID, err := c.getDestinationChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up destination chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	return []string{
		"hermes", "create", "client", srcChainID, dstChainID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME CreateConnections
func (c commander) CreateConnections(pathName, homeDir string) []string {
	srcChainID, err := c.getSourceChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up source chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	dstChainID, err := c.getDestinationChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up destination chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	return []string{
		"hermes", "tx", "connection", srcChainID, dstChainID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME GeneratePath
func (commander) GeneratePath(srcChainID, dstChainID, pathName, homeDir string) []string {
	return []string{
		"hermes", "paths", "new", srcChainID, dstChainID, "--new-client-connection",
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

func (commander) GetChannels(chainID, homeDir string) []string {
	return []string{
		"hermes", "query", "channels", chainID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

func (commander) GetConnections(chainID, homeDir string) []string {
	return []string{
		"hermes", "query", "connections", chainID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME LinkPath
func (commander) LinkPath(pathName, homeDir string) []string {
	return []string{
		"hermes", "tx", "link", pathName,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

func (commander) RestoreKey(chainID, keyName, mnemonic, homeDir string) []string {
	return []string{
		"hermes", "keys", "restore", chainID, "-n", keyName, "-m", mnemonic,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

func (commander) StartRelayer(pathName, homeDir string) []string {
	return []string{
		"hermes", "start",
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME UpdateClients
func (c commander) UpdateClients(pathName, homeDir string) []string {
	dstChainID, err := c.getDestinationChainIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up destination chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	dstClientID, err := c.getDestinationClientIDFromPath(pathName, homeDir)
	if err != nil {
		c.log.Error("Failed to lookup up destination chainid for path %s: %v", zap.String("path", pathName), zap.Error(err))
		return []string{}
	}

	return []string{
		"hermes", "update", "client", dstChainID, dstClientID,
		"-c", filepath.Join(homeDir, "config.toml"),
		"-j",
	}
}

// FIXME ConfigContent
func (commander) ConfigContent(ctx context.Context, cfg ibc.ChainConfig, keyName, rpcAddr, grpcAddr, websocketAddr string) ([]byte, error) {
	cosmosRelayerChainConfig := ChainConfigToHermesChainConfig(cfg, keyName, rpcAddr, grpcAddr, websocketAddr)
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

// FIXME ParseAddKeyOutput
func (commander) ParseAddKeyOutput(stdout, stderr string) (ibc.RelayerWallet, error) {
	var wallet ibc.RelayerWallet
	err := json.Unmarshal([]byte(stdout), &wallet)
	return wallet, err
}

// FIXME ParseGetChannelsOutput
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

// FIXME ParseGetConnectionsOutput
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

// TODO getSourceChainIDFromPath
func (c commander) getSourceChainIDFromPath(pathName, homedir string) (string, error) {
	return "", nil
}

// TODO getSourceChainIDFromPath
func (c commander) getDestinationChainIDFromPath(pathName, homedir string) (string, error) {
	return "", nil
}

// TODO getSourceChainIDFromPath
func (c commander) getDestinationClientIDFromPath(pathName, homedir string) (string, error) {
	return "", nil
}

// TODO getSourceChainIDFromPath
func (c commander) getSourcePortIDFromPath(pathName, homedir string) (string, error) {
	return "", nil
}

// FIXME Init
func (commander) Init(homeDir string) []string {
	return []string{
		"hermes", "config", "init",
		"--home", homeDir,
	}
}
