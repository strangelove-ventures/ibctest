package ibctest_test

import (
	"context"
	"testing"
	"time"

	"github.com/strangelove-ventures/ibctest"
	"github.com/strangelove-ventures/ibctest/chain/cosmos"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/test"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"golang.org/x/sync/errgroup"
)

const haltHeight = uint64(10)
const blocksAfterUpgrade = uint64(10)

func testCosmosChainUpgrade(t *testing.T, chainName, initialVersion, upgradeVersion string) {
	ctx := context.Background()

	home := ibctest.TempDir(t)
	client, network := ibctest.DockerSetup(t)

	cfg, err := ibctest.BuiltinChainConfig(chainName)
	require.NoError(t, err)

	cfg.Images[0].Version = initialVersion
	cfg.ChainID = "chain-1"

	chain := cosmos.NewCosmosChain(t.Name(), cfg, 4, 1, zaptest.NewLogger(t))

	err = chain.Initialize(t.Name(), home, client, network, ibc.HaltHeight(haltHeight))
	require.NoError(t, err, "error initializing chain")

	err = chain.Start(t.Name(), ctx)
	require.NoError(t, err, "error starting chain")

	// TODO make upgrade proposal

	timeoutCtx, timeoutCtxCancel := context.WithTimeout(ctx, time.Second*45)
	defer timeoutCtxCancel()

	err = test.WaitForBlocks(timeoutCtx, int(haltHeight)+1, chain)
	require.Error(t, err, "chain did not halt at halt height")

	height, err := chain.Height(ctx)
	require.NoError(t, err, "error fetching height after halt")

	require.Equal(t, haltHeight, height, "height is not equal to halt height")

	var eg errgroup.Group
	for _, n := range chain.ChainNodes {
		n := n
		eg.Go(func() error {
			if err := n.StopContainer(ctx); err != nil {
				return err
			}
			return n.RemoveContainer(ctx)
		})
	}
	require.NoError(t, eg.Wait(), "error stopping node(s)")

	chain.UpgradeVersion(client, upgradeVersion)

	for _, n := range chain.ChainNodes {
		n := n
		eg.Go(func() error {
			if err := n.SetHaltHeight(ctx, 0); err != nil {
				return err
			}
			if err := n.CreateNodeContainer(ctx); err != nil {
				return err
			}
			return n.StartContainer(ctx)
		})
	}
	require.NoError(t, eg.Wait(), "error starting upgraded node(s)")

	timeoutCtx, timeoutCtxCancel = context.WithTimeout(ctx, time.Second*45)
	defer timeoutCtxCancel()

	err = test.WaitForBlocks(timeoutCtx, int(blocksAfterUpgrade), chain)
	require.NoError(t, err, "chain did not produce blocks after upgrade")

	height, err = chain.Height(ctx)
	require.NoError(t, err, "error fetching height after upgrade")

	require.GreaterOrEqual(t, height, haltHeight+blocksAfterUpgrade, "height is not ")
}

func TestJunoUpgrade(t *testing.T) {
	testCosmosChainUpgrade(t, "juno", "v6.0.0", "v7.0.0")
}
