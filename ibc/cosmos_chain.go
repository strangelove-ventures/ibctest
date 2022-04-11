package ibc

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/cosmos/cosmos-sdk/types"
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
	chainNodes    ChainNodes
}

func NewCosmosChainConfig(name string,
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
	if len(c.chainNodes) > c.numValidators {
		// use first full node
		return c.chainNodes[c.numValidators]
	}
	// use first validator
	return c.chainNodes[0]
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
func (c *CosmosChain) SendIBCTransfer(ctx context.Context, channelID, keyName string, amount WalletAmount, timeout *IBCTimeout) (string, error) {
	return c.getRelayerNode().SendIBCTransfer(ctx, channelID, keyName, amount, timeout)
}

// Implements Chain interface
func (c *CosmosChain) WaitForBlocks(number int64) error {
	return c.getRelayerNode().WaitForBlocks(number)
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

// creates the test node objects required for bootstrapping tests
func (c *CosmosChain) initializeChainNodes(testName, home string,
	pool *dockertest.Pool, networkID string) {
	chainNodes := []*ChainNode{}
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
		chainNodes = append(chainNodes, tn)
	}
	c.chainNodes = chainNodes
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

	validators := c.chainNodes[:c.numValidators]
	fullnodes := c.chainNodes[c.numValidators:]

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
		if err := validator0.AddGenesisAccount(ctx, wallet.Address, []types.Coin{types.Coin{Denom: wallet.Denom, Amount: types.NewInt(wallet.Amount)}}); err != nil {
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
	return c.getRelayerNode().WaitForBlocks(5)
}
