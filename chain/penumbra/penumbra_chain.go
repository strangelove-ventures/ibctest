package penumbra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	volumetypes "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/strangelove-ventures/ibctest/chain/internal/tendermint"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/internal/dockerutil"
	"github.com/strangelove-ventures/ibctest/test"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type PenumbraNode struct {
	TendermintNode  *tendermint.TendermintNode
	PenumbraAppNode *PenumbraAppNode
}

type PenumbraNodes []PenumbraNode

type PenumbraChain struct {
	log           *zap.Logger
	testName      string
	cfg           ibc.ChainConfig
	numValidators int
	numFullNodes  int
	PenumbraNodes PenumbraNodes
}

type PenumbraValidatorDefinition struct {
	IdentityKey    string                           `json:"identity_key"`
	ConsensusKey   string                           `json:"consensus_key"`
	Name           string                           `json:"name"`
	Website        string                           `json:"website"`
	Description    string                           `json:"description"`
	FundingStreams []PenumbraValidatorFundingStream `json:"funding_streams"`
	SequenceNumber int64                            `json:"sequence_number"`
}

type PenumbraValidatorFundingStream struct {
	Address string `json:"address"`
	RateBPS int64  `json:"rate_bps"`
}

type PenumbraGenesisAppStateAllocation struct {
	Amount  int64  `json:"amount"`
	Denom   string `json:"denom"`
	Address string `json:"address"`
}

func NewPenumbraChainConfig() ibc.ChainConfig {
	return ibc.ChainConfig{
		Type:           "penumbra",
		Name:           "penumbra",
		Bech32Prefix:   "penumbra",
		Denom:          "upenumbra",
		GasPrices:      "0.0upenumbra",
		GasAdjustment:  1.3,
		TrustingPeriod: "672h",
		Images: []ibc.DockerImage{
			{
				Repository: "ghcr.io/strangelove-ventures/heighliner/tendermint",
			},
			{
				Repository: "ghcr.io/strangelove-ventures/heighliner/penumbra",
			},
		},
		Bin: "tendermint",
	}
}

func NewPenumbraChain(log *zap.Logger, testName string, chainConfig ibc.ChainConfig, numValidators int, numFullNodes int) *PenumbraChain {
	return &PenumbraChain{
		log:           log,
		testName:      testName,
		cfg:           chainConfig,
		numValidators: numValidators,
		numFullNodes:  numFullNodes,
	}
}

func (c *PenumbraChain) Acknowledgements(ctx context.Context, height uint64) ([]ibc.PacketAcknowledgement, error) {
	panic("implement me")
}

func (c *PenumbraChain) Timeouts(ctx context.Context, height uint64) ([]ibc.PacketTimeout, error) {
	panic("implement me")
}

// Implements Chain interface
func (c *PenumbraChain) Config() ibc.ChainConfig {
	return c.cfg
}

// Implements Chain interface
func (c *PenumbraChain) Initialize(testName string, homeDirectory string, cli *client.Client, networkID string) error {
	return c.initializeChainNodes(testName, homeDirectory, cli, networkID)
}

// Exec implements chain interface.
func (c *PenumbraChain) Exec(ctx context.Context, cmd []string, env []string) (stdout, stderr []byte, err error) {
	return c.getRelayerNode().PenumbraAppNode.Exec(ctx, cmd, env)
}

func (c *PenumbraChain) getRelayerNode() PenumbraNode {
	if len(c.PenumbraNodes) > c.numValidators {
		// use first full node
		return c.PenumbraNodes[c.numValidators]
	}
	// use first validator
	return c.PenumbraNodes[0]
}

// Implements Chain interface
func (c *PenumbraChain) GetRPCAddress() string {
	return fmt.Sprintf("http://%s:26657", c.getRelayerNode().TendermintNode.HostName())
}

// Implements Chain interface
func (c *PenumbraChain) GetGRPCAddress() string {
	return fmt.Sprintf("%s:9090", c.getRelayerNode().TendermintNode.HostName())
}

// GetHostRPCAddress returns the address of the RPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *PenumbraChain) GetHostRPCAddress() string {
	return "http://" + c.getRelayerNode().PenumbraAppNode.hostRPCPort
}

// GetHostGRPCAddress returns the address of the gRPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *PenumbraChain) GetHostGRPCAddress() string {
	return c.getRelayerNode().PenumbraAppNode.hostGRPCPort
}

func (c *PenumbraChain) HomeDir() string {
	panic(errors.New("HomeDir not implemented yet"))
}

// Implements Chain interface
func (c *PenumbraChain) CreateKey(ctx context.Context, keyName string) error {
	return c.getRelayerNode().PenumbraAppNode.CreateKey(ctx, keyName)
}

func (c *PenumbraChain) RecoverKey(ctx context.Context, name, mnemonic string) error {
	return fmt.Errorf("RecoverKey not implemented for PenumbraChain")
}

// Implements Chain interface
func (c *PenumbraChain) GetAddress(ctx context.Context, keyName string) ([]byte, error) {
	return c.getRelayerNode().PenumbraAppNode.GetAddress(ctx, keyName)
}

// Implements Chain interface
func (c *PenumbraChain) SendFunds(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	return c.getRelayerNode().PenumbraAppNode.SendFunds(ctx, keyName, amount)
}

// Implements Chain interface
func (c *PenumbraChain) SendIBCTransfer(ctx context.Context, channelID, keyName string, amount ibc.WalletAmount, timeout *ibc.IBCTimeout) (ibc.Tx, error) {
	return c.getRelayerNode().PenumbraAppNode.SendIBCTransfer(ctx, channelID, keyName, amount, timeout)
}

// Implements Chain interface
func (c *PenumbraChain) InstantiateContract(ctx context.Context, keyName string, amount ibc.WalletAmount, fileName, initMessage string, needsNoAdminFlag bool) (string, error) {
	// NOOP
	return "", errors.New("not yet implemented")
}

// Implements Chain interface
func (c *PenumbraChain) ExecuteContract(ctx context.Context, keyName string, contractAddress string, message string) error {
	// NOOP
	return errors.New("not yet implemented")
}

// Implements Chain interface
func (c *PenumbraChain) DumpContractState(ctx context.Context, contractAddress string, height int64) (*ibc.DumpContractStateResponse, error) {
	// NOOP
	return nil, errors.New("not yet implemented")
}

// Implements Chain interface
func (c *PenumbraChain) ExportState(ctx context.Context, height int64) (string, error) {
	return "", errors.New("not yet implemented")
}

// Implements Chain interface
func (c *PenumbraChain) CreatePool(ctx context.Context, keyName string, contractAddress string, swapFee float64, exitFee float64, assets []ibc.WalletAmount) error {
	// NOOP
	return errors.New("not yet implemented")
}

func (c *PenumbraChain) Height(ctx context.Context) (uint64, error) {
	return c.getRelayerNode().TendermintNode.Height(ctx)
}

// Implements Chain interface
func (c *PenumbraChain) GetBalance(ctx context.Context, address string, denom string) (int64, error) {
	return -1, errors.New("not yet implemented")
}

// Implements Chain interface
func (c *PenumbraChain) GetGasFeesInNativeDenom(gasPaid int64) int64 {
	gasPrice, _ := strconv.ParseFloat(strings.Replace(c.cfg.GasPrices, c.cfg.Denom, "", 1), 64)
	fees := float64(gasPaid) * gasPrice
	return int64(fees)
}

// creates the test node objects required for bootstrapping tests
func (c *PenumbraChain) initializeChainNodes(
	testName, home string,
	cli *client.Client,
	networkID string,
) error {
	ctx := context.TODO()
	penumbraNodes := []PenumbraNode{}
	count := c.numValidators + c.numFullNodes
	chainCfg := c.Config()
	for _, image := range chainCfg.Images {
		rc, err := cli.ImagePull(
			ctx,
			image.Repository+":"+image.Version,
			types.ImagePullOptions{},
		)
		if err != nil {
			c.log.Error("Failed to pull image",
				zap.Error(err),
				zap.String("repository", image.Repository),
				zap.String("tag", image.Version),
			)
		} else {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
	}
	for i := 0; i < count; i++ {
		tn := &tendermint.TendermintNode{Log: c.log, Index: i, Chain: c,
			DockerClient: cli, NetworkID: networkID, TestName: testName, Image: chainCfg.Images[0]}

		tv, err := cli.VolumeCreate(ctx, volumetypes.VolumeCreateBody{
			Labels: map[string]string{
				dockerutil.CleanupLabel: testName,

				dockerutil.NodeOwnerLabel: tn.Name(),
			},
		})
		if err != nil {
			return fmt.Errorf("creating tendermint volume: %w", err)
		}
		tn.VolumeName = tv.Name
		if err := dockerutil.SetVolumeOwner(ctx, dockerutil.VolumeOwnerOptions{
			Log: c.log,

			Client: cli,

			VolumeName: tn.VolumeName,
			ImageRef:   tn.Image.Ref(),
			TestName:   tn.TestName,
		}); err != nil {
			return fmt.Errorf("set tendermint volume owner: %w", err)
		}

		pn := &PenumbraAppNode{log: c.log, Index: i, Chain: c,
			DockerClient: cli, NetworkID: networkID, TestName: testName, Image: chainCfg.Images[1]}
		pv, err := cli.VolumeCreate(ctx, volumetypes.VolumeCreateBody{
			Labels: map[string]string{
				dockerutil.CleanupLabel: testName,

				dockerutil.NodeOwnerLabel: pn.Name(),
			},
		})
		if err != nil {
			return fmt.Errorf("creating penumbra volume: %w", err)
		}
		pn.VolumeName = pv.Name
		if err := dockerutil.SetVolumeOwner(ctx, dockerutil.VolumeOwnerOptions{
			Log: c.log,

			Client: cli,

			VolumeName: pn.VolumeName,
			ImageRef:   pn.Image.Ref(),
			TestName:   pn.TestName,
		}); err != nil {
			return fmt.Errorf("set penumbra volume owner: %w", err)
		}

		penumbraNodes = append(penumbraNodes, PenumbraNode{TendermintNode: tn, PenumbraAppNode: pn})
	}
	c.PenumbraNodes = penumbraNodes

	return nil
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

func (c *PenumbraChain) Start(testName string, ctx context.Context, additionalGenesisWallets ...ibc.WalletAmount) error {
	validators := c.PenumbraNodes[:c.numValidators]
	fullnodes := c.PenumbraNodes[c.numValidators:]

	chainCfg := c.Config()

	validatorDefinitions := make([]PenumbraValidatorDefinition, len(validators))
	allocations := make([]PenumbraGenesisAppStateAllocation, len(validators)*2)

	eg, egCtx := errgroup.WithContext(ctx)
	for i, v := range validators {
		v := v
		i := i
		eg.Go(func() error {
			if err := v.TendermintNode.InitValidatorFiles(egCtx); err != nil {
				return fmt.Errorf("error initializing validator files: %v", err)
			}
			fr := dockerutil.NewFileRetriever(c.log, v.TendermintNode.DockerClient, v.TendermintNode.TestName)
			privValKeyBytes, err := fr.SingleFileContent(egCtx, v.TendermintNode.VolumeName, "config/priv_validator_key.json")
			if err != nil {
				return fmt.Errorf("error reading tendermint privval key file: %v", err)
			}
			privValKey := tendermint.PrivValidatorKeyFile{}
			if err := json.Unmarshal(privValKeyBytes, &privValKey); err != nil {
				return fmt.Errorf("error unmarshaling tendermint privval key: %v", err)
			}
			if err := v.PenumbraAppNode.CreateKey(egCtx, valKey); err != nil {
				return fmt.Errorf("error generating wallet on penumbra node: %v", err)
			}
			if err := v.PenumbraAppNode.InitValidatorFile(egCtx); err != nil {
				return fmt.Errorf("error initializing validator template on penumbra node: %v", err)
			}

			// In all likelihood, the PenumbraAppNode and TendermintNode have the same DockerClient and TestName,
			// but instantiate a new FileRetriever to be defensive.
			fr = dockerutil.NewFileRetriever(c.log, v.PenumbraAppNode.DockerClient, v.PenumbraAppNode.TestName)
			validatorTemplateDefinitionFileBytes, err := fr.SingleFileContent(egCtx, v.PenumbraAppNode.VolumeName, "validator.json")
			if err != nil {
				return fmt.Errorf("error reading validator definition template file: %v", err)
			}
			validatorTemplateDefinition := PenumbraValidatorDefinition{}
			if err := json.Unmarshal(validatorTemplateDefinitionFileBytes, &validatorTemplateDefinition); err != nil {
				return fmt.Errorf("error unmarshaling validator definition template key: %v", err)
			}
			validatorTemplateDefinition.ConsensusKey = privValKey.PubKey.Value
			validatorTemplateDefinition.Name = fmt.Sprintf("validator-%d", i)
			validatorTemplateDefinition.Description = fmt.Sprintf("validator-%d description", i)
			validatorTemplateDefinition.Website = fmt.Sprintf("https://validator-%d", i)

			// Assign validatorDefinitions and allocations at fixed indices to avoid data races across the error group's goroutines.
			validatorDefinitions[i] = validatorTemplateDefinition

			// self delegation
			allocations[2*i] = PenumbraGenesisAppStateAllocation{
				Amount:  100_000_000_000,
				Denom:   fmt.Sprintf("udelegation_%s", validatorTemplateDefinition.IdentityKey),
				Address: validatorTemplateDefinition.FundingStreams[0].Address,
			}
			// liquid
			allocations[2*i+1] = PenumbraGenesisAppStateAllocation{
				Amount:  1_000_000_000_000,
				Denom:   chainCfg.Denom,
				Address: validatorTemplateDefinition.FundingStreams[0].Address,
			}

			return nil
		})
	}

	for _, wallet := range additionalGenesisWallets {
		allocations = append(allocations, PenumbraGenesisAppStateAllocation{
			Address: wallet.Address,
			Denom:   wallet.Denom,
			Amount:  wallet.Amount,
		})
	}

	for _, n := range fullnodes {
		n := n
		eg.Go(func() error { return n.TendermintNode.InitFullNodeFiles(egCtx) })
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("waiting to init full nodes' files: %w", err)
	}

	firstVal := c.PenumbraNodes[0]
	if err := firstVal.PenumbraAppNode.GenerateGenesisFile(ctx, chainCfg.ChainID, validatorDefinitions, allocations); err != nil {
		return fmt.Errorf("generating genesis file: %w", err)
	}

	// penumbra generate-testnet right now overwrites new validator keys
	eg, egCtx = errgroup.WithContext(ctx)
	for i, val := range c.PenumbraNodes[:c.numValidators] {
		i := i
		val := val
		// Use an errgroup to save some time doing many concurrent copies inside containers.
		eg.Go(func() error {
			firstValPrivKeyRelPath := fmt.Sprintf("node%d/tendermint/config/priv_validator_key.json", i)

			fr := dockerutil.NewFileRetriever(c.log, firstVal.PenumbraAppNode.DockerClient, firstVal.PenumbraAppNode.TestName)
			pk, err := fr.SingleFileContent(egCtx, firstVal.PenumbraAppNode.VolumeName, firstValPrivKeyRelPath)
			if err != nil {
				return fmt.Errorf("getting validator private key content: %w", err)
			}

			fw := dockerutil.NewFileWriter(c.log, val.PenumbraAppNode.DockerClient, val.PenumbraAppNode.TestName)
			if err := fw.WriteFile(egCtx, val.TendermintNode.VolumeName, "config/priv_validator_key.json", pk); err != nil {
				return fmt.Errorf("overwriting priv_validator_key.json: %w", err)
			}

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	return c.start(ctx)
}

// Bootstraps the chain and starts it from genesis
func (c *PenumbraChain) start(ctx context.Context) error {
	// Copy the penumbra genesis to all tendermint nodes.
	genesisContent, err := c.PenumbraNodes[0].PenumbraAppNode.genesisFileContent(ctx)
	if err != nil {
		return err
	}

	tendermintNodes := make([]*tendermint.TendermintNode, len(c.PenumbraNodes))
	for i, node := range c.PenumbraNodes {
		tendermintNodes[i] = node.TendermintNode
		if err := node.TendermintNode.OverwriteGenesisFile(ctx, genesisContent); err != nil {
			return err
		}
	}

	tmNodes := tendermint.TendermintNodes(tendermintNodes)

	if err := tmNodes.LogGenesisHashes(ctx); err != nil {
		return err
	}

	eg, egCtx := errgroup.WithContext(ctx)
	for _, n := range c.PenumbraNodes {
		n := n
		eg.Go(func() error {
			return n.TendermintNode.CreateNodeContainer(
				egCtx,
				fmt.Sprintf("--proxy-app=tcp://%s:26658", n.PenumbraAppNode.HostName()),
				"--rpc.laddr=tcp://0.0.0.0:26657",
			)
		})
		eg.Go(func() error {
			return n.PenumbraAppNode.CreateNodeContainer(egCtx)
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	eg, egCtx = errgroup.WithContext(ctx)
	for _, n := range c.PenumbraNodes {
		n := n
		c.log.Info("Starting tendermint container", zap.String("container", n.TendermintNode.Name()))
		eg.Go(func() error {
			peers := tmNodes.PeerString(egCtx, n.TendermintNode)
			if err := n.TendermintNode.SetConfigAndPeers(egCtx, peers); err != nil {
				return err
			}
			return n.TendermintNode.StartContainer(egCtx)
		})
		c.log.Info("Starting penumbra container", zap.String("container", n.PenumbraAppNode.Name()))
		eg.Go(func() error {
			return n.PenumbraAppNode.StartContainer(egCtx)
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Wait for 5 blocks before considering the chains "started"
	return test.WaitForBlocks(ctx, 5, c.getRelayerNode().TendermintNode)
}
