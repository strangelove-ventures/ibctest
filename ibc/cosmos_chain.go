package ibc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	authTx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	bankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type CosmosChain struct {
	testName      string
	cfg           ChainConfig
	numValidators int
	numFullNodes  int
	ChainNodes    ChainNodes
}

func NewCosmosChainConfig(name string,
	dockerImage string,
	binary string,
	bech32Prefix string,
	denom string,
	gasPrices string,
	gasAdjustment float64,
	trustingPeriod string) ChainConfig {
	return ChainConfig{
		Type:           "cosmos",
		Name:           name,
		Bech32Prefix:   bech32Prefix,
		Denom:          denom,
		GasPrices:      gasPrices,
		GasAdjustment:  gasAdjustment,
		TrustingPeriod: trustingPeriod,
		Repository:     dockerImage,
		Bin:            binary,
	}
}

func NewCosmosHeighlinerChainConfig(name string,
	binary string,
	bech32Prefix string,
	denom string,
	gasPrices string,
	gasAdjustment float64,
	trustingPeriod string) ChainConfig {
	return ChainConfig{
		Type:           "cosmos",
		Name:           name,
		Bech32Prefix:   bech32Prefix,
		Denom:          denom,
		GasPrices:      gasPrices,
		GasAdjustment:  gasAdjustment,
		TrustingPeriod: trustingPeriod,
		Repository:     fmt.Sprintf("ghcr.io/strangelove-ventures/heighliner/%s", name),
		Bin:            binary,
	}
}

func NewCosmosChain(testName string, chainConfig ChainConfig, numValidators int, numFullNodes int) *CosmosChain {
	return &CosmosChain{
		testName:      testName,
		cfg:           chainConfig,
		numValidators: numValidators,
		numFullNodes:  numFullNodes,
	}
}

// Implements Chain interface
func (c *CosmosChain) Config() ChainConfig {
	return c.cfg
}

// Implements Chain interface
func (c *CosmosChain) Initialize(testName string, homeDirectory string, dockerPool *dockertest.Pool, networkID string) error {
	c.initializeChainNodes(testName, homeDirectory, dockerPool, networkID)
	return nil
}

func (c *CosmosChain) getRelayerNode() *ChainNode {
	if len(c.ChainNodes) > c.numValidators {
		// use first full node
		return c.ChainNodes[c.numValidators]
	}
	// use first validator
	return c.ChainNodes[0]
}

// Implements Chain interface
func (c *CosmosChain) GetRPCAddress() string {
	return fmt.Sprintf("http://%s:26657", c.getRelayerNode().Name())
}

// Implements Chain interface
func (c *CosmosChain) GetGRPCAddress() string {
	return fmt.Sprintf("%s:9090", c.getRelayerNode().Name())
}

// Implements Chain interface
func (c *CosmosChain) CreateKey(ctx context.Context, keyName string) error {
	return c.getRelayerNode().CreateKey(ctx, keyName)
}

// Implements Chain interface
func (c *CosmosChain) GetAddress(keyName string) ([]byte, error) {
	keyInfo, err := c.getRelayerNode().Keybase().Key(keyName)
	if err != nil {
		return []byte{}, err
	}

	return keyInfo.GetAddress().Bytes(), nil
}

// Implements Chain interface
func (c *CosmosChain) SendFunds(ctx context.Context, keyName string, amount WalletAmount) error {
	return c.getRelayerNode().SendFunds(ctx, keyName, amount)
}

// Implements Chain interface
func (c *CosmosChain) SendIBCTransfer(ctx context.Context, channelID, keyName string, amount WalletAmount, timeout *IBCTimeout) (string, error) {
	return c.getRelayerNode().SendIBCTransfer(ctx, channelID, keyName, amount, timeout)
}

// Implements Chain interface
func (c *CosmosChain) InstantiateContract(ctx context.Context, keyName string, amount WalletAmount, fileName, initMessage string, needsNoAdminFlag bool) (string, error) {
	return c.getRelayerNode().InstantiateContract(ctx, keyName, amount, fileName, initMessage, needsNoAdminFlag)
}

// Implements Chain interface
func (c *CosmosChain) ExecuteContract(ctx context.Context, keyName string, contractAddress string, message string) error {
	return c.getRelayerNode().ExecuteContract(ctx, keyName, contractAddress, message)
}

// Implements Chain interface
func (c *CosmosChain) DumpContractState(ctx context.Context, contractAddress string, height int64) (*DumpContractStateResponse, error) {
	return c.getRelayerNode().DumpContractState(ctx, contractAddress, height)
}

// Implements Chain interface
func (c *CosmosChain) ExportState(ctx context.Context, height int64) (string, error) {
	return c.getRelayerNode().ExportState(ctx, height)
}

// Implements Chain interface
func (c *CosmosChain) CreatePool(ctx context.Context, keyName string, contractAddress string, swapFee float64, exitFee float64, assets []WalletAmount) error {
	return c.getRelayerNode().CreatePool(ctx, keyName, contractAddress, swapFee, exitFee, assets)
}

// Implements Chain interface
func (c *CosmosChain) WaitForBlocks(number int64) (int64, error) {
	return c.getRelayerNode().WaitForBlocks(number)
}

func (c *CosmosChain) Height() (int64, error) {
	return c.getRelayerNode().Height()
}

// Implements Chain interface
func (c *CosmosChain) GetBalance(ctx context.Context, address string, denom string) (int64, error) {
	params := &bankTypes.QueryBalanceRequest{Address: address, Denom: denom}
	grpcAddress := GetHostPort(c.getRelayerNode().Container, grpcPort)
	conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	queryClient := bankTypes.NewQueryClient(conn)
	res, err := queryClient.Balance(ctx, params)

	if err != nil {
		return 0, err
	}

	return res.Balance.Amount.Int64(), nil
}

func (c *CosmosChain) GetTransaction(ctx context.Context, txHash string) (*types.TxResponse, error) {
	return authTx.QueryTx(c.getRelayerNode().CliContext(), txHash)
}

func (c *CosmosChain) GetGasFeesInNativeDenom(gasPaid int64) int64 {
	gasPrice, _ := strconv.ParseFloat(strings.Replace(c.cfg.GasPrices, c.cfg.Denom, "", 1), 64)
	fees := float64(gasPaid) * gasPrice
	return int64(fees)
}

// creates the test node objects required for bootstrapping tests
func (c *CosmosChain) initializeChainNodes(testName, home string,
	pool *dockertest.Pool, networkID string) {
	ChainNodes := []*ChainNode{}
	count := c.numValidators + c.numFullNodes
	chainCfg := c.Config()
	err := pool.Client.PullImage(docker.PullImageOptions{
		Repository: chainCfg.Repository,
		Tag:        chainCfg.Version,
	}, docker.AuthConfiguration{})
	if err != nil {
		fmt.Printf("error pulling image: %v", err)
	}
	for i := 0; i < count; i++ {
		tn := &ChainNode{Home: home, Index: i, Chain: c,
			Pool: pool, NetworkID: networkID, testName: testName}
		tn.MkDir()
		ChainNodes = append(ChainNodes, tn)
	}
	c.ChainNodes = ChainNodes
}

type GenesisValidatorPubKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type GenesisValidators struct {
	Address string                 `json:"address"`
	Name    string                 `json:"name"`
	Power   string                 `json:"power"`
	PubKey  GenesisValidatorPubKey `json:"pub_key"`
}
type GenesisFile struct {
	Validators []GenesisValidators `json:"validators"`
}

type ValidatorWithIntPower struct {
	Address      string
	Power        int64
	PubKeyBase64 string
}

// Bootstraps the chain and starts it from genesis
func (c *CosmosChain) StartWithGenesisFile(testName string, ctx context.Context, home string, pool *dockertest.Pool, networkID string, genesisFilePath string) error {
	// copy genesis file to tmp path for modification
	genesisTmpFilePath := path.Join(c.getRelayerNode().Dir(), "genesis_tmp.json")
	if _, err := copy(genesisFilePath, genesisTmpFilePath); err != nil {
		return err
	}

	chainCfg := c.Config()

	genesisJsonBytes, err := ioutil.ReadFile(genesisTmpFilePath)
	if err != nil {
		return err
	}

	genesisFile := GenesisFile{}
	if err := json.Unmarshal(genesisJsonBytes, &genesisFile); err != nil {
		return err
	}

	genesisValidators := genesisFile.Validators
	totalPower := int64(0)

	validatorsWithPower := make([]ValidatorWithIntPower, 0)

	for _, genesisValidator := range genesisValidators {
		power, err := strconv.ParseInt(genesisValidator.Power, 10, 64)
		if err != nil {
			return err
		}
		totalPower += power
		validatorsWithPower = append(validatorsWithPower, ValidatorWithIntPower{
			Address:      genesisValidator.Address,
			Power:        power,
			PubKeyBase64: genesisValidator.PubKey.Value,
		})
	}

	sort.Slice(validatorsWithPower, func(i, j int) bool {
		return validatorsWithPower[i].Power > validatorsWithPower[j].Power
	})

	twoThirdsConsensus := int64(math.Ceil(float64(totalPower) * 2 / 3))
	totalConsensus := int64(0)

	c.ChainNodes = []*ChainNode{}

	for i, validator := range validatorsWithPower {
		tn := &ChainNode{Home: home, Index: i, Chain: c,
			Pool: pool, NetworkID: networkID, testName: testName}
		tn.MkDir()
		c.ChainNodes = append(c.ChainNodes, tn)

		// just need to get pubkey here
		// don't care about what goes into this node's genesis file since it will be overwritten with the modified one
		if err := tn.InitHomeFolder(ctx); err != nil {
			return err
		}

		testNodePubKeyJsonBytes, err := ioutil.ReadFile(tn.PrivValKeyFilePath())
		if err != nil {
			return err
		}

		testNodePrivValFile := PrivValidatorKeyFile{}
		if err := json.Unmarshal(testNodePubKeyJsonBytes, &testNodePrivValFile); err != nil {
			return err
		}

		// modify genesis file overwriting validators address with the one generated for this test node
		genesisJsonBytes = bytes.ReplaceAll(genesisJsonBytes, []byte(validator.Address), []byte(testNodePrivValFile.Address))

		// modify genesis file overwriting validators base64 pub_key.value with the one generated for this test node
		genesisJsonBytes = bytes.ReplaceAll(genesisJsonBytes, []byte(validator.PubKeyBase64), []byte(testNodePrivValFile.PubKey.Value))

		existingValAddressBytes, err := hex.DecodeString(validator.Address)
		if err != nil {
			return err
		}

		testNodeAddressBytes, err := hex.DecodeString(testNodePrivValFile.Address)
		if err != nil {
			return err
		}

		valConsPrefix := fmt.Sprintf("%svalcons", chainCfg.Bech32Prefix)

		existingValBech32ValConsAddress, err := bech32.ConvertAndEncode(valConsPrefix, existingValAddressBytes)
		if err != nil {
			return err
		}

		testNodeBech32ValConsAddress, err := bech32.ConvertAndEncode(valConsPrefix, testNodeAddressBytes)
		if err != nil {
			return err
		}

		genesisJsonBytes = bytes.ReplaceAll(genesisJsonBytes, []byte(existingValBech32ValConsAddress), []byte(testNodeBech32ValConsAddress))

		totalConsensus += validator.Power

		if totalConsensus > twoThirdsConsensus {
			break
		}
	}

	for i := 0; i < len(c.ChainNodes); i++ {
		if err := ioutil.WriteFile(c.ChainNodes[i].GenesisFilePath(), genesisJsonBytes, 0644); err != nil { //nolint
			return err
		}
	}

	if err := ChainNodes(c.ChainNodes).LogGenesisHashes(); err != nil {
		return err
	}

	var eg errgroup.Group

	for _, n := range c.ChainNodes {
		n := n
		eg.Go(func() error {
			return n.CreateNodeContainer()
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	peers := ChainNodes(c.ChainNodes).PeerString()

	for _, n := range c.ChainNodes {
		n.SetValidatorConfigAndPeers(peers)
	}

	for _, n := range c.ChainNodes {
		n := n
		fmt.Printf("{%s} => starting container...\n", n.Name())
		if err := n.StartContainer(ctx); err != nil {
			return err
		}
	}

	time.Sleep(2 * time.Hour)

	// Wait for 5 blocks before considering the chains "started"
	_, err = c.getRelayerNode().WaitForBlocks(5)
	return err
}

// Bootstraps the chain and starts it from genesis
func (c *CosmosChain) Start(testName string, ctx context.Context, additionalGenesisWallets []WalletAmount) error {
	var eg errgroup.Group

	chainCfg := c.Config()

	genesisAmount := types.Coin{
		Amount: types.NewInt(1000000000000),
		Denom:  chainCfg.Denom,
	}

	genesisStakeAmount := types.Coin{
		Amount: types.NewInt(1000000000000),
		Denom:  "stake",
	}

	genesisSelfDelegation := types.Coin{
		Amount: types.NewInt(100000000000),
		Denom:  "stake",
	}

	genesisAmounts := []types.Coin{genesisAmount, genesisStakeAmount}

	validators := c.ChainNodes[:c.numValidators]
	fullnodes := c.ChainNodes[c.numValidators:]

	// sign gentx for each validator
	for _, v := range validators {
		v := v
		eg.Go(func() error { return v.InitValidatorFiles(ctx, &chainCfg, genesisAmounts, genesisSelfDelegation) })
	}

	// just initialize folder for any full nodes
	for _, n := range fullnodes {
		n := n
		eg.Go(func() error { return n.InitFullNodeFiles(ctx) })
	}

	// wait for this to finish
	if err := eg.Wait(); err != nil {
		return err
	}

	// for the validators we need to collect the gentxs and the accounts
	// to the first node's genesis file
	validator0 := validators[0]
	for i := 1; i < len(validators); i++ {
		validatorN := validators[i]
		n0key, err := validatorN.GetKey(valKey)
		if err != nil {
			return err
		}

		bech32, err := types.Bech32ifyAddressBytes(chainCfg.Bech32Prefix, n0key.GetAddress().Bytes())
		if err != nil {
			return err
		}

		if err := validator0.AddGenesisAccount(ctx, bech32, genesisAmounts); err != nil {
			return err
		}
		nNid, err := validatorN.NodeID()
		if err != nil {
			return err
		}
		oldPath := path.Join(validatorN.Dir(), "config", "gentx", fmt.Sprintf("gentx-%s.json", nNid))
		newPath := path.Join(validator0.Dir(), "config", "gentx", fmt.Sprintf("gentx-%s.json", nNid))
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
	}

	for _, wallet := range additionalGenesisWallets {
		if err := validator0.AddGenesisAccount(ctx, wallet.Address, []types.Coin{{Denom: wallet.Denom, Amount: types.NewInt(wallet.Amount)}}); err != nil {
			return err
		}
	}

	if err := validator0.CollectGentxs(ctx); err != nil {
		return err
	}

	genbz, err := ioutil.ReadFile(validator0.GenesisFilePath())
	if err != nil {
		return err
	}

	nodes := validators
	nodes = append(nodes, fullnodes...)

	for i := 1; i < len(nodes); i++ {
		if err := ioutil.WriteFile(nodes[i].GenesisFilePath(), genbz, 0644); err != nil { //nolint
			return err
		}
	}

	if err := ChainNodes(nodes).LogGenesisHashes(); err != nil {
		return err
	}

	for _, n := range nodes {
		n := n
		eg.Go(func() error {
			return n.CreateNodeContainer()
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	peers := ChainNodes(nodes).PeerString()

	for _, n := range nodes {
		n := n
		fmt.Printf("{%s} => starting container...\n", n.Name())
		eg.Go(func() error {
			n.SetValidatorConfigAndPeers(peers)
			return n.StartContainer(ctx)
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Wait for 5 blocks before considering the chains "started"
	_, err = c.getRelayerNode().WaitForBlocks(5)
	return err
}

// RegisterInterchainAccount will register an interchain account, on behalf of the controller chain (the calling chain),
// on the controller chain (the dst chain).
func (c *CosmosChain) RegisterInterchainAccount(ctx context.Context, address, connectionID string) (string, error) {
	return c.getRelayerNode().RegisterICA(ctx, address, connectionID)
}

// QueryInterchainAccount will query the interchain account that was created on behalf of the specified address.
func (c *CosmosChain) QueryInterchainAccount(ctx context.Context, connectionID, address string) (string, error) {
	return c.getRelayerNode().QueryICA(ctx, connectionID, address)
}

// SendICABankTransfer will send a bank transfer msg from the fromAddr to the toAddr for the given amount and denom.
func (c *CosmosChain) SendICABankTransfer(ctx context.Context, connectionID, fromAddr, toAddr, denom string, amount int64) error {
	return c.getRelayerNode().SendICABankTransfer(ctx, connectionID, fromAddr, toAddr, denom, amount)
}
