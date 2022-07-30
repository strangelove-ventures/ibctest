package ibctest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/strangelove-ventures/ibctest"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/test"
	"github.com/strangelove-ventures/ibctest/testreporter"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestInterchainAccounts(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	home := ibctest.TempDir(t)
	client, network := ibctest.DockerSetup(t)

	rep := testreporter.NewNopReporter()
	eRep := rep.RelayerExecReporter(t)

	ctx := context.Background()

	// Get both chains
	cf := ibctest.NewBuiltinChainFactory(zaptest.NewLogger(t), []*ibctest.ChainSpec{
		{Name: "icad", Version: "master"},
		{Name: "icad", Version: "master"},
	})

	chains, err := cf.Chains(t.Name())
	require.NoError(t, err)

	chain1, chain2 := chains[0], chains[1]

	r := ibctest.NewBuiltinRelayerFactory(ibc.CosmosRly, zaptest.NewLogger(t)).Build(
		t, client, network,
	)

	const pathName = "test-path"

	ic := ibctest.NewInterchain().
		AddChain(chain1).
		AddChain(chain2).
		AddRelayer(r, "relayer").
		AddLink(ibctest.InterchainLink{
			Chain1:  chain1,
			Chain2:  chain2,
			Relayer: r,
			Path:    pathName,
		})

	require.NoError(t, ic.Build(ctx, eRep, ibctest.InterchainBuildOptions{
		TestName:  t.Name(),
		HomeDir:   home,
		Client:    client,
		NetworkID: network,

		SkipPathCreation: true,
	}))

	const userFunds = int64(10_000_000_000)
	users := ibctest.GetAndFundTestUsers(t, ctx, t.Name(), userFunds, chain1)
	chain1User := users[0]

	// Generate path
	err = r.GeneratePath(ctx, eRep, chain1.Config().ChainID, chain2.Config().ChainID, pathName)
	require.NoError(t, err)

	// Create new clients
	err = r.CreateClients(ctx, eRep, pathName)
	require.NoError(t, err)

	err = test.WaitForBlocks(ctx, 5, chain1, chain2)
	require.NoError(t, err)

	// Create a new connection
	err = r.CreateConnections(ctx, eRep, pathName)
	require.NoError(t, err)

	err = test.WaitForBlocks(ctx, 5, chain1, chain2)
	require.NoError(t, err)

	// Query for the newly created connection
	connections, err := r.GetConnections(ctx, eRep, chain1.Config().ChainID)
	require.NoError(t, err)
	require.Equal(t, 1, len(connections))

	// Not really great, consider exposing a function for retrieving a nodes addr like below
	host := strings.Split(chain1.GetRPCAddress(), "//")[1]
	nodeAddr := fmt.Sprintf("tcp://%s", host)

	// Register a new interchain account
	registerICA := []string{
		chain1.Config().Bin, "tx", "intertx", "register",
		"--from", chain1User.Bech32Address(chain1.Config().Bech32Prefix),
		"--connection-id", connections[0].ID,
		"--chain-id", chain1.Config().ChainID,
		"--home", chain1.HomeDir(),
		"--node", nodeAddr,
		"--keyring-backend", keyring.BackendTest,
		"-y",
	}
	_, _, err = chain1.Exec(ctx, registerICA, nil)
	require.NoError(t, err)

	// Start the relayer on both paths
	//startRly := []string{
	//	"rly", "start", pathName, "--debug", "--home",
	//}
	err = r.StartRelayer(ctx, eRep, pathName)
	require.NoError(t, err)

	t.Cleanup(
		func() {
			err := r.StopRelayer(ctx, eRep)
			if err != nil {
				t.Logf("an error occured while stopping the relayer: %s", err)
			}
		},
	)

	// Wait for relayer to start up and finish channel handshake
	err = test.WaitForBlocks(ctx, 15, chain1, chain2)
	require.NoError(t, err)

	// Query for the newly registered interchain account
	queryICA := []string{
		chain1.Config().Bin, "query", "intertx", "interchainaccounts", connections[0].ID, chain1User.Bech32Address(chain1.Config().Bech32Prefix),
		"--chain-id", chain1.Config().ChainID,
		"--home", chain1.HomeDir(),
		"--node", nodeAddr,
	}
	stdout, _, err := chain1.Exec(ctx, queryICA, nil)
	require.NoError(t, err)

	// at this point stdout should look like this:
	// interchain_account_address: cosmos1p76n3mnanllea4d3av0v0e42tjj03cae06xq8fwn9at587rqp23qvxsv0j
	// we split the string at the : and then just grab the address.
	parts := strings.SplitN(string(stdout), ":", 2)
	require.Equal(t, 2, len(parts))

	icaAddr := strings.TrimSpace(parts[1])
	require.NotEqual(t, "", icaAddr)
}

/*
// SendICABankTransfer builds a bank transfer message for a specified address and sends it to the specified
// interchain account.
func (tn *ChainNode) SendICABankTransfer(ctx context.Context, connectionID, fromAddr string, amount ibc.WalletAmount) error {
	msg, err := json.Marshal(map[string]any{
		"@type":        "/cosmos.bank.v1beta1.MsgSend",
		"from_address": fromAddr,
		"to_address":   amount.Address,
		"amount": []map[string]any{
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
*/
