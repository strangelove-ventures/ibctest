package test

import (
	"testing"

	"github.com/strangelove-ventures/ibc-test-framework/ibc"
	"github.com/stretchr/testify/require"
)

// These tests are run by CI

func getTestChains(t *testing.T) (ibc.Chain, ibc.Chain) {
	numValidatorsPerChain := 4
	numFullNodesPerChain := 1

	srcChain, err := ibc.GetChain(t.Name(), "gaia", "v6.0.4", "cosmoshub-1004", numValidatorsPerChain, numFullNodesPerChain)
	require.NoError(t, err)
	dstChain, err := ibc.GetChain(t.Name(), "osmosis", "v7.0.4", "osmosis-1001", numValidatorsPerChain, numFullNodesPerChain)
	require.NoError(t, err)

	return srcChain, dstChain
}

// queued packet with default timeout should be relayed
func TestRelayPacket(t *testing.T) {
	srcChain, dstChain := getTestChains(t)
	relayerImplementation := ibc.CosmosRly

	require.NoError(t, ibc.IBCTestCase{}.RelayPacketTest(t.Name(), srcChain, dstChain, relayerImplementation))
}

// queued packet with no timeout should be relayed
func TestNoTimeout(t *testing.T) {
	srcChain, dstChain := getTestChains(t)
	relayerImplementation := ibc.CosmosRly

	require.NoError(t, ibc.IBCTestCase{}.RelayPacketTestNoTimeout(t.Name(), srcChain, dstChain, relayerImplementation))
}

// queued packet with relative height timeout that expires should not be relayed
func TestHeightTimeout(t *testing.T) {
	srcChain, dstChain := getTestChains(t)
	relayerImplementation := ibc.CosmosRly

	require.NoError(t, ibc.IBCTestCase{}.RelayPacketTestHeightTimeout(t.Name(), srcChain, dstChain, relayerImplementation))
}

// queued packet with relative timestamp timeout (ns) that expires should not be relayed
func TestTimestampTimeout(t *testing.T) {
	t.Skip() // TODO turn this back on once fixed in cosmos relayer
	srcChain, dstChain := getTestChains(t)
	relayerImplementation := ibc.CosmosRly

	require.NoError(t, ibc.IBCTestCase{}.RelayPacketTestTimestampTimeout(t.Name(), srcChain, dstChain, relayerImplementation))
}
