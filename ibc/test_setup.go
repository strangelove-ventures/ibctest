package ibc

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
	"golang.org/x/sync/errgroup"
)

type RelayerImplementation int64

const (
	CosmosRly RelayerImplementation = iota
	Hermes
)

const (
	srcAccountKeyName  = "src-chain"
	dstAccountKeyName  = "dst-chain"
	userAccountKeyName = "user"
	testPathName       = "test-path"
)

// RandLowerCaseLetterString returns a lowercase letter string of given length
func RandLowerCaseLetterString(length int) string {
	chars := []rune("abcdefghijklmnopqrstuvwxyz")
	var b strings.Builder
	for i := 0; i < length; i++ {
		i, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b.WriteRune(chars[i.Int64()])
	}
	return b.String()
}

func SetupTestRun(testName string) (context.Context, string, *dockertest.Pool, string, func(), error) {
	home, err := ioutil.TempDir("", "")
	ctx := context.Background()
	if err != nil {
		return ctx, "", nil, "", nil, err
	}

	pool, err := dockertest.NewPool("")
	if err != nil {
		return ctx, "", nil, "", nil, err
	}

	network, err := CreateTestNetwork(pool, fmt.Sprintf("ibc-test-framework-%s", RandLowerCaseLetterString(8)), testName)
	if err != nil {
		return ctx, "", nil, "", nil, err
	}

	return ctx, home, pool, network.ID, Cleanup(testName, pool, home), nil
}

type User struct {
	SrcChainAddress string
	DstChainAddress string
	KeyName         string
}

// startup both chains and relayer
// creates wallets in the relayer for src and dst chain
// funds relayer src and dst wallets on respective chain in genesis
// creates a user account on the src chain (separate fullnode)
// funds user account on src chain in genesis
func StartChainsAndRelayer(
	testName string,
	ctx context.Context,
	pool *dockertest.Pool,
	networkID string,
	home string,
	srcChain Chain,
	dstChain Chain,
	relayerImplementation RelayerImplementation,
	preRelayerStart func(channels []ChannelOutput, user User) error,
) ([]ChannelOutput, User, func(), error) {
	var relayerImpl Relayer
	switch relayerImplementation {
	case CosmosRly:
		relayerImpl = NewCosmosRelayerFromChains(
			testName,
			srcChain,
			dstChain,
			pool,
			networkID,
			home,
		)
	case Hermes:
		// not yet supported
	}

	errResponse := func(err error) ([]ChannelOutput, User, func(), error) {
		return []ChannelOutput{}, User{}, nil, err
	}

	if err := srcChain.Initialize(testName, home, pool, networkID); err != nil {
		return errResponse(err)
	}
	if err := dstChain.Initialize(testName, home, pool, networkID); err != nil {
		return errResponse(err)
	}

	srcChainCfg := srcChain.Config()
	dstChainCfg := dstChain.Config()

	if err := relayerImpl.AddChainConfiguration(ctx, srcChainCfg, srcAccountKeyName,
		srcChain.GetRPCAddress(), srcChain.GetGRPCAddress()); err != nil {
		return errResponse(err)
	}

	if err := relayerImpl.AddChainConfiguration(ctx, dstChainCfg, dstAccountKeyName,
		dstChain.GetRPCAddress(), dstChain.GetGRPCAddress()); err != nil {
		return errResponse(err)
	}

	srcRelayerWallet, err := relayerImpl.AddKey(ctx, srcChain.Config().ChainID, srcAccountKeyName)
	if err != nil {
		return errResponse(err)
	}
	dstRelayerWallet, err := relayerImpl.AddKey(ctx, dstChain.Config().ChainID, dstAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	srcAccount := srcRelayerWallet.Address
	dstAccount := dstRelayerWallet.Address

	if err := relayerImpl.GeneratePath(ctx, srcChainCfg.ChainID, dstChainCfg.ChainID, testPathName); err != nil {
		return errResponse(err)
	}

	// Fund relayer account on src chain
	srcWallet := WalletAmount{
		Address: srcAccount,
		Denom:   srcChainCfg.Denom,
		Amount:  10000000,
	}

	// Fund relayer account on dst chain
	dstWallet := WalletAmount{
		Address: dstAccount,
		Denom:   dstChainCfg.Denom,
		Amount:  10000000,
	}

	// Generate key to be used for "user" that will execute IBC transaction
	if err := srcChain.CreateKey(ctx, userAccountKeyName); err != nil {
		return errResponse(err)
	}
	userAccountAddressBytes, err := srcChain.GetAddress(userAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	userAccountSrc, err := types.Bech32ifyAddressBytes(srcChainCfg.Bech32Prefix, userAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	userAccountDst, err := types.Bech32ifyAddressBytes(dstChainCfg.Bech32Prefix, userAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	user := User{
		KeyName:         userAccountKeyName,
		SrcChainAddress: userAccountSrc,
		DstChainAddress: userAccountDst,
	}

	// Fund user account on src chain in order to relay from src to dst
	userWalletSrc := WalletAmount{
		Address: userAccountSrc,
		Denom:   srcChainCfg.Denom,
		Amount:  100000000,
	}

	// start chains from genesis, wait until they are producing blocks
	chainsGenesisWaitGroup := errgroup.Group{}
	chainsGenesisWaitGroup.Go(func() error {
		return srcChain.Start(testName, ctx, []WalletAmount{srcWallet, userWalletSrc})
	})
	chainsGenesisWaitGroup.Go(func() error {
		return dstChain.Start(testName, ctx, []WalletAmount{dstWallet})
	})

	if err := chainsGenesisWaitGroup.Wait(); err != nil {
		return errResponse(err)
	}

	if err := relayerImpl.LinkPath(ctx, testPathName); err != nil {
		return errResponse(err)
	}

	channels, err := relayerImpl.GetChannels(ctx, srcChainCfg.ChainID)
	if err != nil {
		return errResponse(err)
	}
	if len(channels) != 1 {
		return errResponse(fmt.Errorf("channel count invalid. expected: 1, actual: %d", len(channels)))
	}

	if preRelayerStart != nil {
		if err := preRelayerStart(channels, user); err != nil {
			return errResponse(err)
		}
	}

	if err := relayerImpl.StartRelayer(ctx, testPathName); err != nil {
		return errResponse(err)
	}

	// wait for relayer to start up
	time.Sleep(5 * time.Second)

	relayerCleanup := func() {
		err := relayerImpl.StopRelayer(ctx)
		if err != nil {
			fmt.Printf("error stopping relayer: %v\n", err)
		}
	}

	return channels, user, relayerCleanup, nil
}

func WaitForBlocks(srcChain Chain, dstChain Chain, blocksToWait int64) error {
	chainsConsecutiveBlocksWaitGroup := errgroup.Group{}
	chainsConsecutiveBlocksWaitGroup.Go(func() error {
		return srcChain.WaitForBlocks(blocksToWait)
	})
	chainsConsecutiveBlocksWaitGroup.Go(func() error {
		return dstChain.WaitForBlocks(blocksToWait)
	})
	return chainsConsecutiveBlocksWaitGroup.Wait()
}

// GetHostPort returns a resource's published port with an address.
func GetHostPort(cont *docker.Container, portID string) string {
	if cont == nil || cont.NetworkSettings == nil {
		return ""
	}

	m, ok := cont.NetworkSettings.Ports[docker.Port(portID)]
	if !ok || len(m) == 0 {
		return ""
	}

	ip := m[0].HostIP
	if ip == "0.0.0.0" {
		ip = "localhost"
	}
	return net.JoinHostPort(ip, m[0].HostPort)
}

func CreateTestNetwork(pool *dockertest.Pool, name string, testName string) (*docker.Network, error) {
	return pool.Client.CreateNetwork(docker.CreateNetworkOptions{
		Name:           name,
		Options:        map[string]interface{}{},
		Labels:         map[string]string{"ibc-test": testName},
		CheckDuplicate: true,
		Internal:       false,
		EnableIPv6:     false,
		Context:        context.Background(),
	})
}

// Cleanup will clean up Docker containers, networks, and the other various config files generated in testing
func Cleanup(testName string, pool *dockertest.Pool, testDir string) func() {
	return func() {
		cont, _ := pool.Client.ListContainers(docker.ListContainersOptions{All: true})
		ctx := context.Background()
		for _, c := range cont {
			for k, v := range c.Labels {
				if k == "ibc-test" && v == testName {
					_ = pool.Client.StopContainer(c.ID, 10)
					_, _ = pool.Client.WaitContainerWithContext(c.ID, ctx)
					stdout := new(bytes.Buffer)
					stderr := new(bytes.Buffer)
					_ = pool.Client.Logs(docker.LogsOptions{Context: ctx, Container: c.ID, OutputStream: stdout, ErrorStream: stderr, Stdout: true, Stderr: true, Tail: "50", Follow: false, Timestamps: false})
					names := strings.Join(c.Names, ",")
					fmt.Printf("{%s} - stdout:\n%s\n{%s} - stderr:\n%s\n", names, stdout, names, stderr)
					_ = pool.Client.RemoveContainer(docker.RemoveContainerOptions{ID: c.ID})
				}
			}
		}
		nets, _ := pool.Client.ListNetworks()
		for _, n := range nets {
			for k, v := range n.Labels {
				if k == "ibc-test" && v == testName {
					_ = pool.Client.RemoveNetwork(n.ID)
				}
			}
		}
		_ = os.RemoveAll(testDir)
	}
}
