package cosmos

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/internal/blockdb"
	"github.com/strangelove-ventures/ibctest/internal/dockerutil"
	"github.com/strangelove-ventures/ibctest/test"
	tmconfig "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/p2p"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	libclient "github.com/tendermint/tendermint/rpc/jsonrpc/client"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ChainNode represents a node in the test network that is being created
type ChainNode struct {
	Home         string
	Index        int
	Chain        ibc.Chain
	GenesisCoins string
	Validator    bool
	NetworkID    string
	DockerClient *dockerclient.Client
	Client       rpcclient.Client
	TestName     string
	Image        ibc.DockerImage

	lock sync.Mutex
	log  *zap.Logger

	containerID string

	// Ports set during StartContainer.
	hostRPCPort  string
	hostGRPCPort string
}

// ChainNodes is a collection of ChainNode
type ChainNodes []*ChainNode

const (
	valKey      = "validator"
	blockTime   = 2 // seconds
	p2pPort     = "26656/tcp"
	rpcPort     = "26657/tcp"
	grpcPort    = "9090/tcp"
	apiPort     = "1317/tcp"
	privValPort = "1234/tcp"
)

var (
	sentryPorts = nat.PortSet{
		nat.Port(p2pPort):     {},
		nat.Port(rpcPort):     {},
		nat.Port(grpcPort):    {},
		nat.Port(apiPort):     {},
		nat.Port(privValPort): {},
	}
)

// NewClient creates and assigns a new Tendermint RPC client to the ChainNode
func (tn *ChainNode) NewClient(addr string) error {
	httpClient, err := libclient.DefaultHTTPClient(addr)
	if err != nil {
		return err
	}

	httpClient.Timeout = 10 * time.Second
	rpcClient, err := rpchttp.NewWithClient(addr, "/websocket", httpClient)
	if err != nil {
		return err
	}

	tn.Client = rpcClient
	return nil
}

// CliContext creates a new Cosmos SDK client context
func (tn *ChainNode) CliContext() client.Context {
	return client.Context{
		Client:            tn.Client,
		ChainID:           tn.Chain.Config().ChainID,
		InterfaceRegistry: defaultEncoding.InterfaceRegistry,
		Input:             os.Stdin,
		Output:            os.Stdout,
		OutputFormat:      "json",
		LegacyAmino:       defaultEncoding.Amino,
		TxConfig:          defaultEncoding.TxConfig,
	}
}

// Name of the test node container
func (tn *ChainNode) Name() string {
	return fmt.Sprintf("node-%d-%s-%s", tn.Index, tn.Chain.Config().ChainID, dockerutil.SanitizeContainerName(tn.TestName))
}

// hostname of the test node container
func (tn *ChainNode) HostName() string {
	return dockerutil.CondenseHostName(tn.Name())
}

// Dir is the directory where the test node files are stored
func (tn *ChainNode) Dir() string {
	return filepath.Join(tn.Home, tn.Name())
}

// MkDir creates the directory for the testnode
func (tn *ChainNode) MkDir() {
	if err := os.MkdirAll(tn.Dir(), 0755); err != nil {
		panic(err)
	}
}

// GentxPath returns the path to the gentx for a node
func (tn *ChainNode) GentxPath() (string, error) {
	id, err := tn.NodeID()
	return filepath.Join(tn.Dir(), "config", "gentx", fmt.Sprintf("gentx-%s.json", id)), err
}

func (tn *ChainNode) GenesisFilePath() string {
	return filepath.Join(tn.Dir(), "config", "genesis.json")
}

type PrivValidatorKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type PrivValidatorKeyFile struct {
	Address string           `json:"address"`
	PubKey  PrivValidatorKey `json:"pub_key"`
	PrivKey PrivValidatorKey `json:"priv_key"`
}

func (tn *ChainNode) PrivValKeyFilePath() string {
	return filepath.Join(tn.Dir(), "config", "priv_validator_key.json")
}

func (tn *ChainNode) TMConfigPath() string {
	return filepath.Join(tn.Dir(), "config", "config.toml")
}

// Bind returns the home folder bind point for running the node
func (tn *ChainNode) Bind() []string {
	return []string{fmt.Sprintf("%s:%s", tn.Dir(), tn.HomeDir())}
}

func (tn *ChainNode) HomeDir() string {
	return filepath.Join("/tmp", tn.Chain.Config().Name)
}

// Keybase returns the keyring for a given node
func (tn *ChainNode) Keybase() keyring.Keyring {
	kr, err := keyring.New("", keyring.BackendTest, tn.Dir(), os.Stdin)
	if err != nil {
		panic(err)
	}
	return kr
}

// SetValidatorConfigAndPeers modifies the config for a validator node to start a chain
func (tn *ChainNode) SetValidatorConfigAndPeers(peers string) {
	// Pull default config
	cfg := tmconfig.DefaultConfig()

	// change config to include everything needed
	applyConfigChanges(cfg, peers)

	// overwrite with the new config
	tmconfig.WriteConfigFile(tn.TMConfigPath(), cfg)
}

func (tn *ChainNode) Height(ctx context.Context) (uint64, error) {
	res, err := tn.Client.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("tendermint rpc client status: %w", err)
	}
	height := res.SyncInfo.LatestBlockHeight
	return uint64(height), nil
}

// FindTxs implements blockdb.BlockSaver.
func (tn *ChainNode) FindTxs(ctx context.Context, height uint64) ([]blockdb.Tx, error) {
	h := int64(height)
	blockRes, err := tn.Client.Block(ctx, &h)
	if err != nil {
		return nil, err
	}
	txs := make([]blockdb.Tx, len(blockRes.Block.Txs))
	for i, tx := range blockRes.Block.Txs {
		// Store the raw transaction data first, in case decoding/encoding fails.
		txs[i].Data = tx

		sdkTx, err := decodeTX(tx)
		if err != nil {
			tn.logger().Info("Failed to decode tx", zap.Uint64("height", height), zap.Error(err))
			continue
		}
		b, err := encodeTxToJSON(sdkTx)
		if err != nil {
			tn.logger().Info("Failed to marshal tx to json", zap.Uint64("height", height), zap.Error(err))
			continue
		}
		txs[i].Data = b

		// Request the transaction directly in order to get the tendermint events.
		txRes, err := tn.Client.Tx(ctx, tx.Hash(), false)
		if err != nil {
			tn.logger().Info("Failed to retrieve tx", zap.Uint64("height", height), zap.Error(err))
			continue
		}

		txs[i].Events = make([]blockdb.Event, len(txRes.TxResult.Events))
		for j, e := range txRes.TxResult.Events {
			attrs := make([]blockdb.EventAttribute, len(e.Attributes))
			for k, attr := range e.Attributes {
				attrs[k] = blockdb.EventAttribute{
					Key:   string(attr.Key),
					Value: string(attr.Value),
				}
			}
			txs[i].Events[j] = blockdb.Event{
				Type:       e.Type,
				Attributes: attrs,
			}
		}
	}

	return txs, nil
}

func applyConfigChanges(cfg *tmconfig.Config, peers string) {
	// turn down blocktimes to make the chain faster
	cfg.Consensus.TimeoutCommit = time.Duration(blockTime) * time.Second
	cfg.Consensus.TimeoutPropose = time.Duration(blockTime) * time.Second

	// Open up rpc address
	cfg.RPC.ListenAddress = "tcp://0.0.0.0:26657"

	// Allow for some p2p weirdness
	cfg.P2P.AllowDuplicateIP = true
	cfg.P2P.AddrBookStrict = false

	// Set log level to info
	cfg.BaseConfig.LogLevel = "info"

	// set persistent peer nodes
	cfg.P2P.PersistentPeers = peers
}

// CondenseMoniker fits a moniker into the cosmos character limit for monikers.
// If the moniker already fits, it is returned unmodified.
// Otherwise, the middle is truncated, and a hash is appended to the end
// in case the only unique data was in the middle.
func CondenseMoniker(m string) string {
	if len(m) <= stakingtypes.MaxMonikerLength {
		return m
	}

	// Get the hash suffix, a 32-bit uint formatted in base36.
	// fnv32 was chosen because a 32-bit number ought to be sufficient
	// as a distinguishing suffix, and it will be short enough so that
	// less of the middle will be truncated to fit in the character limit.
	// It's also non-cryptographic, not that this function will ever be a bottleneck in tests.
	h := fnv.New32()
	h.Write([]byte(m))
	suffix := "-" + strconv.FormatUint(uint64(h.Sum32()), 36)

	wantLen := stakingtypes.MaxMonikerLength - len(suffix)

	// Half of the want length, minus 2 to account for half of the ... we add in the middle.
	keepLen := (wantLen / 2) - 2

	return m[:keepLen] + "..." + m[len(m)-keepLen:] + suffix
}

// InitHomeFolder initializes a home folder for the given node
func (tn *ChainNode) InitHomeFolder(ctx context.Context) error {
	command := []string{tn.Chain.Config().Bin, "init", CondenseMoniker(tn.Name()),
		"--chain-id", tn.Chain.Config().ChainID,
		"--home", tn.HomeDir(),
	}
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

// CreateKey creates a key in the keyring backend test for the given node
func (tn *ChainNode) CreateKey(ctx context.Context, name string) error {
	command := []string{tn.Chain.Config().Bin, "keys", "add", name,
		"--keyring-backend", keyring.BackendTest,
		"--output", "json",
		"--home", tn.HomeDir(),
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

// RecoverKey restores a key from a given mnemonic.
func (tn *ChainNode) RecoverKey(ctx context.Context, keyName, mnemonic string) error {
	command := []string{
		"sh",
		"-c",
		fmt.Sprintf(`echo %q | %s keys add %s --recover --keyring-backend %s --home %s --output json`, mnemonic, tn.Chain.Config().Bin, keyName, keyring.BackendTest, tn.HomeDir()),
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

// AddGenesisAccount adds a genesis account for each key
func (tn *ChainNode) AddGenesisAccount(ctx context.Context, address string, genesisAmount []types.Coin) error {
	amount := ""
	for i, coin := range genesisAmount {
		if i != 0 {
			amount += ","
		}
		amount += fmt.Sprintf("%d%s", coin.Amount.Int64(), coin.Denom)
	}
	command := []string{tn.Chain.Config().Bin, "add-genesis-account", address, amount,
		"--home", tn.HomeDir(),
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()

	// Adding a genesis account should complete instantly,
	// so use a 1-minute timeout to more quickly detect if Docker has locked up.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

// Gentx generates the gentx for a given node
func (tn *ChainNode) Gentx(ctx context.Context, name string, genesisSelfDelegation types.Coin) error {
	command := []string{tn.Chain.Config().Bin, "gentx", valKey, fmt.Sprintf("%d%s", genesisSelfDelegation.Amount.Int64(), genesisSelfDelegation.Denom),
		"--keyring-backend", keyring.BackendTest,
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

// CollectGentxs runs collect gentxs on the node's home folders
func (tn *ChainNode) CollectGentxs(ctx context.Context) error {
	command := []string{tn.Chain.Config().Bin, "collect-gentxs",
		"--home", tn.HomeDir(),
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

type IBCTransferTx struct {
	TxHash string `json:"txhash"`
}

func (tn *ChainNode) SendIBCTransfer(ctx context.Context, channelID string, keyName string, amount ibc.WalletAmount, timeout *ibc.IBCTimeout) (string, error) {
	command := []string{tn.Chain.Config().Bin, "tx", "ibc-transfer", "transfer", "transfer", channelID,
		amount.Address, fmt.Sprintf("%d%s", amount.Amount, amount.Denom),
		"--keyring-backend", keyring.BackendTest,
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--from", keyName,
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	if timeout != nil {
		if timeout.NanoSeconds > 0 {
			command = append(command, "--packet-timeout-timestamp", fmt.Sprint(timeout.NanoSeconds))
		} else if timeout.Height > 0 {
			command = append(command, "--packet-timeout-height", fmt.Sprintf("0-%d", timeout.Height))
		}
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	stdout, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}
	err = test.WaitForBlocks(ctx, 2, tn)
	if err != nil {
		return "", fmt.Errorf("wait for blocks: %w", err)
	}
	output := IBCTransferTx{}
	err = json.Unmarshal([]byte(stdout), &output)
	return output.TxHash, err
}

func (tn *ChainNode) SendFunds(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	command := []string{tn.Chain.Config().Bin, "tx", "bank", "send", keyName,
		amount.Address, fmt.Sprintf("%d%s", amount.Amount, amount.Denom),
		"--keyring-backend", keyring.BackendTest,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	return tn.ExecThenWaitForBlocks(ctx, command)
}

func (tn *ChainNode) ExecThenWaitForBlocks(ctx context.Context, command []string) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return err
	}
	return test.WaitForBlocks(ctx, 2, tn)
}

type InstantiateContractAttribute struct {
	Value string `json:"value"`
}

type InstantiateContractEvent struct {
	Attributes []InstantiateContractAttribute `json:"attributes"`
}

type InstantiateContractLog struct {
	Events []InstantiateContractEvent `json:"event"`
}

type InstantiateContractResponse struct {
	Logs []InstantiateContractLog `json:"log"`
}

type QueryContractResponse struct {
	Contracts []string `json:"contracts"`
}

type CodeInfo struct {
	CodeID string `json:"code_id"`
}
type CodeInfosResponse struct {
	CodeInfos []CodeInfo `json:"code_infos"`
}

func (tn *ChainNode) InstantiateContract(ctx context.Context, keyName string, amount ibc.WalletAmount, fileName, initMessage string, needsNoAdminFlag bool) (string, error) {
	_, file := filepath.Split(fileName)
	newFilePath := filepath.Join(tn.Dir(), file)
	newFilePathContainer := filepath.Join(tn.HomeDir(), file)
	if _, err := dockerutil.CopyFile(fileName, newFilePath); err != nil {
		return "", err
	}

	command := []string{tn.Chain.Config().Bin, "tx", "wasm", "store", newFilePathContainer,
		"--from", keyName,
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--keyring-backend", keyring.BackendTest,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	tn.lock.Lock()
	defer tn.lock.Unlock()
	_, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}

	err = test.WaitForBlocks(ctx, 5, tn.Chain)
	if err != nil {
		return "", fmt.Errorf("wait for blocks: %w", err)
	}

	command = []string{tn.Chain.Config().Bin,
		"query", "wasm", "list-code", "--reverse",
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}

	stdout, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}

	res := CodeInfosResponse{}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		return "", err
	}

	codeID := res.CodeInfos[0].CodeID

	command = []string{tn.Chain.Config().Bin,
		"tx", "wasm", "instantiate", codeID, initMessage,
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--label", "satoshi-test",
		"--from", keyName,
		"--keyring-backend", keyring.BackendTest,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}

	if needsNoAdminFlag {
		command = append(command, "--no-admin")
	}

	_, _, err = tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}

	err = test.WaitForBlocks(ctx, 5, tn.Chain)
	if err != nil {
		return "", fmt.Errorf("wait for blocks: %w", err)
	}

	command = []string{tn.Chain.Config().Bin,
		"query", "wasm", "list-contract-by-code", codeID,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}

	stdout, _, err = tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}

	contactsRes := QueryContractResponse{}
	if err := json.Unmarshal([]byte(stdout), &contactsRes); err != nil {
		return "", err
	}

	contractAddress := contactsRes.Contracts[len(contactsRes.Contracts)-1]
	return contractAddress, nil
}

func (tn *ChainNode) ExecuteContract(ctx context.Context, keyName string, contractAddress string, message string) error {
	command := []string{tn.Chain.Config().Bin,
		"tx", "wasm", "execute", contractAddress, message,
		"--from", keyName,
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--keyring-backend", keyring.BackendTest,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	return tn.ExecThenWaitForBlocks(ctx, command)
}

// TODO use dazzle or read/write config from go with go-toml or BurntSushi/toml
func (tn *ChainNode) SetHaltHeight(ctx context.Context, height uint64) error {
	command := []string{
		"sed", "-i",
		fmt.Sprintf("/^halt-height = .*/ s//halt-height = \"%d\"/", height),
		filepath.Join(tn.HomeDir(), "config", "app.toml"),
	}
	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

func (tn *ChainNode) DumpContractState(ctx context.Context, contractAddress string, height int64) (*ibc.DumpContractStateResponse, error) {
	command := []string{tn.Chain.Config().Bin,
		"query", "wasm", "contract-state", "all", contractAddress,
		"--height", fmt.Sprint(height),
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	stdout, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return nil, err
	}

	res := &ibc.DumpContractStateResponse{}
	if err := json.Unmarshal([]byte(stdout), res); err != nil {
		return nil, err
	}
	return res, nil
}

func (tn *ChainNode) ExportState(ctx context.Context, height int64) (string, error) {
	command := []string{tn.Chain.Config().Bin,
		"export",
		"--height", fmt.Sprint(height),
		"--home", tn.HomeDir(),
	}
	_, stderr, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}
	// output comes to stderr for some reason
	return string(stderr), nil
}

func (tn *ChainNode) UnsafeResetAll(ctx context.Context) error {
	command := []string{tn.Chain.Config().Bin,
		"unsafe-reset-all",
		"--home", tn.HomeDir(),
	}

	_, _, err := tn.Exec(ctx, command, nil)
	return err
}

func (tn *ChainNode) CreatePool(ctx context.Context, keyName string, contractAddress string, swapFee float64, exitFee float64, assets []ibc.WalletAmount) error {
	// TODO generate --pool-file
	poolFilePath := "TODO"
	command := []string{tn.Chain.Config().Bin,
		"tx", "gamm", "create-pool",
		"--pool-file", poolFilePath,
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--from", keyName,
		"--keyring-backend", keyring.BackendTest,
		"--node", fmt.Sprintf("tcp://%s:26657", tn.HostName()),
		"--output", "json",
		"-y",
		"--home", tn.HomeDir(),
		"--chain-id", tn.Chain.Config().ChainID,
	}
	return tn.ExecThenWaitForBlocks(ctx, command)
}

func (tn *ChainNode) CreateNodeContainer(ctx context.Context) error {
	chainCfg := tn.Chain.Config()
	cmd := []string{chainCfg.Bin, "start", "--home", tn.HomeDir(), "--x-crisis-skip-assert-invariants"}
	if chainCfg.NoHostMount {
		cmd = []string{"sh", "-c", fmt.Sprintf("cp -r %s %s_nomnt && %s start --home %s_nomnt --x-crisis-skip-assert-invariants", tn.HomeDir(), tn.HomeDir(), chainCfg.Bin, tn.HomeDir())}
	}
	imageRef := tn.Image.Ref()
	tn.logger().
		Info("Running command",
			zap.String("command", strings.Join(cmd, " ")),
			zap.String("container", tn.Name()),
			zap.String("image", imageRef),
		)

	cc, err := tn.DockerClient.ContainerCreate(
		ctx,
		&container.Config{
			Image: imageRef,

			Entrypoint: []string{},
			Cmd:        cmd,

			Hostname: tn.HostName(),
			User:     dockerutil.GetDockerUserString(),

			Labels: map[string]string{dockerutil.CleanupLabel: tn.TestName},

			ExposedPorts: sentryPorts,
		},
		&container.HostConfig{
			Binds:           tn.Bind(),
			PublishAllPorts: true,
			AutoRemove:      false,
			DNS:             []string{},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				tn.NetworkID: {},
			},
		},
		nil,
		tn.Name(),
	)
	if err != nil {
		return err
	}
	tn.containerID = cc.ID
	return nil
}

func (tn *ChainNode) StartContainer(ctx context.Context) error {
	if err := dockerutil.StartContainer(ctx, tn.DockerClient, tn.containerID); err != nil {
		return err
	}

	c, err := tn.DockerClient.ContainerInspect(ctx, tn.containerID)
	if err != nil {
		return err
	}

	// Set the host ports once since they will not change after the container has started.
	tn.hostRPCPort = dockerutil.GetHostPort(c, rpcPort)
	tn.hostGRPCPort = dockerutil.GetHostPort(c, grpcPort)

	tn.logger().Info("Cosmos chain node started", zap.String("container", tn.Name()), zap.String("rpc_port", tn.hostRPCPort))

	err = tn.NewClient("tcp://" + tn.hostRPCPort)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	return retry.Do(func() error {
		stat, err := tn.Client.Status(ctx)
		if err != nil {
			return err
		}
		// TODO: reenable this check, having trouble with it for some reason
		if stat != nil && stat.SyncInfo.CatchingUp {
			return fmt.Errorf("still catching up: height(%d) catching-up(%t)",
				stat.SyncInfo.LatestBlockHeight, stat.SyncInfo.CatchingUp)
		}
		return nil
	}, retry.Context(ctx), retry.Attempts(40), retry.Delay(3*time.Second), retry.DelayType(retry.FixedDelay))
}

func (tn *ChainNode) StopContainer(ctx context.Context) error {
	timeout := 30 * time.Second
	return tn.DockerClient.ContainerStop(ctx, tn.containerID, &timeout)
}

func (tn *ChainNode) RemoveContainer(ctx context.Context) error {
	err := tn.DockerClient.ContainerRemove(ctx, tn.containerID, dockertypes.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("remove container %s: %w", tn.Name(), err)
	}
	return nil
}

// InitValidatorFiles creates the node files and signs a genesis transaction
func (tn *ChainNode) InitValidatorFiles(
	ctx context.Context,
	chainType *ibc.ChainConfig,
	genesisAmounts []types.Coin,
	genesisSelfDelegation types.Coin,
	haltHeight uint64,
) error {
	if err := tn.InitHomeFolder(ctx); err != nil {
		return err
	}
	if err := tn.CreateKey(ctx, valKey); err != nil {
		return err
	}
	if haltHeight != 0 {
		if err := tn.SetHaltHeight(ctx, haltHeight); err != nil {
			return err
		}
	}
	key, err := tn.GetKey(valKey)
	if err != nil {
		return err
	}
	bech32, err := types.Bech32ifyAddressBytes(chainType.Bech32Prefix, key.GetAddress().Bytes())
	if err != nil {
		return err
	}
	if err := tn.AddGenesisAccount(ctx, bech32, genesisAmounts); err != nil {
		return err
	}
	return tn.Gentx(ctx, valKey, genesisSelfDelegation)
}

func (tn *ChainNode) InitFullNodeFiles(ctx context.Context) error {
	return tn.InitHomeFolder(ctx)
}

// NodeID returns the node of a given node
func (tn *ChainNode) NodeID() (string, error) {
	nodeKey, err := p2p.LoadNodeKey(filepath.Join(tn.Dir(), "config", "node_key.json"))
	if err != nil {
		return "", err
	}
	return string(nodeKey.ID()), nil
}

// GetKey gets a key, waiting until it is available
func (tn *ChainNode) GetKey(name string) (info keyring.Info, err error) {
	return info, retry.Do(func() (err error) {
		info, err = tn.Keybase().Key(name)
		return err
	})
}

// PeerString returns the string for connecting the nodes passed in
func (nodes ChainNodes) PeerString() string {
	addrs := make([]string, len(nodes))
	for i, n := range nodes {
		id, err := n.NodeID()
		if err != nil {
			// TODO: would this be better to panic?
			// When would NodeId return an error?
			break
		}
		hostName := n.HostName()
		ps := fmt.Sprintf("%s@%s:26656", id, hostName)
		nodes.logger().Info("Peering",
			zap.String("host_name", hostName),
			zap.String("peer", ps),
			zap.String("container", n.Name()),
		)
		addrs[i] = ps
	}
	return strings.Join(addrs, ",")
}

// LogGenesisHashes logs the genesis hashes for the various nodes
func (nodes ChainNodes) LogGenesisHashes() error {
	for _, n := range nodes {
		gen, err := os.ReadFile(filepath.Join(n.Dir(), "config", "genesis.json"))
		if err != nil {
			return err
		}
		nodes.logger().Info("Genesis", zap.String("hash", fmt.Sprintf("%X", sha256.Sum256(gen))))
	}
	return nil
}

func (nodes ChainNodes) logger() *zap.Logger {
	if len(nodes) == 0 {
		return zap.NewNop()
	}
	return nodes[0].logger()
}

func (tn *ChainNode) Exec(ctx context.Context, cmd []string, env []string) ([]byte, []byte, error) {
	job := dockerutil.NewImage(tn.logger(), tn.DockerClient, tn.NetworkID, tn.TestName, tn.Image.Repository, tn.Image.Version)
	opts := dockerutil.ContainerOptions{
		Env:   env,
		Binds: tn.Bind(),
	}
	return job.Run(ctx, cmd, opts)
}

func (tn *ChainNode) logger() *zap.Logger {
	return tn.log.With(
		zap.String("chain_id", tn.Chain.Config().ChainID),
		zap.String("test", tn.TestName),
	)
}

// RegisterICA will attempt to register an interchain account on the counterparty chain.
func (tn *ChainNode) RegisterICA(ctx context.Context, address, connectionID string) (string, error) {
	command := []string{tn.Chain.Config().Bin, "tx", "intertx", "register",
		"--from", address,
		"--connection-id", connectionID,
		"--chain-id", tn.Chain.Config().ChainID,
		"--home", tn.HomeDir(),
		"--node", fmt.Sprintf("tcp://%s:26657", tn.Name()),
		"--keyring-backend", keyring.BackendTest,
		"-y",
	}

	stdout, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}
	output := IBCTransferTx{}
	err = yaml.Unmarshal([]byte(stdout), &output)
	if err != nil {
		return "", err
	}
	return output.TxHash, nil
}

// QueryICA will query for an interchain account controlled by the specified address on the counterparty chain.
func (tn *ChainNode) QueryICA(ctx context.Context, connectionID, address string) (string, error) {
	command := []string{tn.Chain.Config().Bin, "query", "intertx", "interchainaccounts", connectionID, address,
		"--chain-id", tn.Chain.Config().ChainID,
		"--home", tn.HomeDir(),
		"--node", fmt.Sprintf("tcp://%s:26657", tn.Name())}

	stdout, _, err := tn.Exec(ctx, command, nil)
	if err != nil {
		return "", err
	}

	// at this point stdout should look like this:
	// interchain_account_address: cosmos1p76n3mnanllea4d3av0v0e42tjj03cae06xq8fwn9at587rqp23qvxsv0j
	// we split the string at the : and then just grab the address before returning.
	parts := strings.SplitN(string(stdout), ":", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("malformed stdout from command: %s", stdout)
	}
	return strings.TrimSpace(parts[1]), nil
}

// SendICABankTransfer builds a bank transfer message for a specified address and sends it to the specified
// interchain account.
func (tn *ChainNode) SendICABankTransfer(ctx context.Context, connectionID, fromAddr string, amount ibc.WalletAmount) error {
	msg, err := json.Marshal(map[string]interface{}{
		"@type":        "/cosmos.bank.v1beta1.MsgSend",
		"from_address": fromAddr,
		"to_address":   amount.Address,
		"amount": []map[string]interface{}{
			{
				"denom":  amount.Denom,
				"amount": amount.Amount,
			},
		},
	})
	if err != nil {
		return err
	}

	command := []string{tn.Chain.Config().Bin, "tx", "intertx", "submit", string(msg),
		"--connection-id", connectionID,
		"--from", fromAddr,
		"--chain-id", tn.Chain.Config().ChainID,
		"--home", tn.HomeDir(),
		"--node", fmt.Sprintf("tcp://%s:26657", tn.Name()),
		"--keyring-backend", keyring.BackendTest,
		"-y",
	}

	_, _, err = tn.Exec(ctx, command, nil)
	return err
}
