package ibctest

import (
	"context"
	"strconv"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/strangelove-ventures/ibctest"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/test"
	"github.com/strangelove-ventures/ibctest/testreporter"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestInterchainQueries(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	home := ibctest.TempDir(t)
	client, network := ibctest.DockerSetup(t)

	rep := testreporter.NewNopReporter()
	eRep := rep.RelayerExecReporter(t)

	ctx := context.Background()

	// TODO still need to get a docker image pulled into heighliner for icqd to avoid this manual configuration
	dockerImage := ibc.DockerImage{
		Repository: "icq",
		Version:    "e21dd75",
	}

	cf := ibctest.NewBuiltinChainFactory(zaptest.NewLogger(t), []*ibctest.ChainSpec{
		{ChainName: "test-1", ChainConfig: ibc.ChainConfig{
			Type:           "cosmos",
			Name:           "icq",
			ChainID:        "test-1",
			Images:         []ibc.DockerImage{dockerImage},
			Bin:            "icq",
			Bech32Prefix:   "cosmos",
			Denom:          "atom",
			GasPrices:      "0.00stake",
			TrustingPeriod: "300h",
			GasAdjustment:  1.1,
		}},
		{ChainName: "test-2", ChainConfig: ibc.ChainConfig{
			Type:           "cosmos",
			Name:           "icq",
			ChainID:        "test-2",
			Images:         []ibc.DockerImage{dockerImage},
			Bin:            "icq",
			Bech32Prefix:   "cosmos",
			Denom:          "atom",
			GasPrices:      "0.00stake",
			TrustingPeriod: "300h",
			GasAdjustment:  1.1,
		}},
	})

	chains, err := cf.Chains(t.Name())
	require.NoError(t, err)

	chain1, chain2 := chains[0], chains[1]

	r := ibctest.NewBuiltinRelayerFactory(ibc.CosmosRly, zaptest.NewLogger(t)).Build(
		t, client, network, home,
	)

	const pathName = "test1-test2"
	const relayerName = "relayer"

	ic := ibctest.NewInterchain().
		AddChain(chain1).
		AddChain(chain2).
		AddRelayer(r, relayerName).
		AddLink(ibctest.InterchainLink{
			Chain1:  chain1,
			Chain2:  chain2,
			Relayer: r,
			Path:    pathName,
		})

	// Build the network by initializing and starting the chains and creating an IBC path between them.
	require.NoError(t, ic.Build(ctx, eRep, ibctest.InterchainBuildOptions{
		TestName:  t.Name(),
		HomeDir:   home,
		Client:    client,
		NetworkID: network,

		SkipPathCreation: false,
		CreateChannelOpts: ibc.CreateChannelOptions{
			SourcePortName: "interquery",
			DestPortName:   "icqhost",
			Order:          ibc.Unordered,
			Version:        "icq-1",
		},
	}))

	// Fund user accounts, so we can query balances and make assertions.
	const userFunds = int64(10_000_000_000)
	users := ibctest.GetAndFundTestUsers(t, ctx, t.Name(), userFunds, chain1, chain2)
	user1 := users[0]
	user2 := users[1]

	// Wait a few blocks for user accounts to be created on chain.
	err = test.WaitForBlocks(ctx, 10, chain1, chain2)
	require.NoError(t, err)

	// Query for the recently created channel-id.
	channels, err := r.GetChannels(ctx, eRep, chain1.Config().ChainID)
	require.NoError(t, err)

	// Start the relayer and set the cleanup function.
	err = r.StartRelayer(ctx, eRep, pathName)
	require.NoError(t, err)

	t.Cleanup(
		func() {
			err := r.StopRelayer(ctx, eRep)
			if err != nil {
				t.Logf("an error occured while stopping the relayer: %s", err)
			}
			for _, c := range chains {
				if err = c.Cleanup(ctx); err != nil {
					t.Logf("an error occured while stopping chain %s: %s", c.Config().ChainID, err)
				}
			}
		},
	)

	// Wait a few blocks for the relayer to start.
	err = test.WaitForBlocks(ctx, 5, chain1, chain2)
	require.NoError(t, err)

	// Query for the balances of an account on the counterparty chain using IBC queries.
	chanID := channels[0].Counterparty.ChannelID
	require.NotEqual(t, "", chanID)

	user1Addr := user1.Bech32Address(chain1.Config().Bech32Prefix)
	require.NotEqual(t, "", user1Addr)

	user2Addr := user2.Bech32Address(chain2.Config().Bech32Prefix)
	require.NotEqual(t, "", user2Addr)

	cmd := []string{"icq", "tx", "interquery", "send-query-all-balances", chanID, user2Addr,
		"--node", chain1.GetRPCAddress(),
		"--home", chain1.HomeDir(),
		"--chain-id", chain1.Config().ChainID,
		"--from", user1Addr,
		"--keyring-dir", chain1.HomeDir(),
		"--keyring-backend", keyring.BackendTest,
		"-y",
	}
	stdout, stderr, err := chain1.Exec(ctx, cmd, nil)
	require.NoError(t, err)

	t.Logf("stdout: %s \n", stdout)
	t.Logf("stderr: %s \n", stderr)

	// Wait a few blocks for query to be sent to counterparty.
	t.Log("Waiting for blocks...")

	err = test.WaitForBlocks(ctx, 10, chain1)
	require.NoError(t, err)

	t.Log("Finished waiting for blocks after sending IBC query")

	// Check the results from the IBC query above.
	cmd = []string{"icq", "query", "interquery", "query-state", strconv.Itoa(1),
		"--node", chain1.GetRPCAddress(),
		"--home", chain1.HomeDir(),
		"--chain-id", chain1.Config().ChainID,
	}
	stdout, stderr, err = chain1.Exec(ctx, cmd, nil)
	require.NoError(t, err)

	t.Logf("stdout: %s \n", stdout)
	t.Logf("stderr: %s \n", stderr)
}
