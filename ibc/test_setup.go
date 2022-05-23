package ibc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"reflect"
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

// all methods on this struct have the same signature and are method names that will be called by the CLI
// func (ibc IBCTestCase) TestCaseName(testName string, srcChain Chain, dstChain Chain, relayerImplementation RelayerImplementation) error
type IBCTestCase struct{}

// uses reflection to get test case
func GetTestCase(testCase string) (func(testName string, srcChain Chain, dstChain Chain, relayerImplementation RelayerImplementation) error, error) {
	v := reflect.ValueOf(IBCTestCase{})
	m := v.MethodByName(testCase)

	if m.Kind() != reflect.Func {
		return nil, fmt.Errorf("invalid test case: %s", testCase)
	}

	testCaseFunc := func(testName string, srcChain Chain, dstChain Chain, relayerImplementation RelayerImplementation) error {
		args := []reflect.Value{reflect.ValueOf(testName), reflect.ValueOf(srcChain), reflect.ValueOf(dstChain), reflect.ValueOf(relayerImplementation)}
		result := m.Call(args)
		if len(result) != 1 || !result[0].CanInterface() {
			return errors.New("error reflecting error return var")
		}

		err, _ := result[0].Interface().(error)
		return err
	}

	return testCaseFunc, nil
}

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
	preRelayerStart func([]ChannelOutput, User, User) error,
) (Relayer, []ChannelOutput, *User, *User, func(), error) {
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
		// TODO not yet supported
	}

	errResponse := func(err error) (Relayer, []ChannelOutput, *User, *User, func(), error) {
		return nil, []ChannelOutput{}, nil, nil, nil, err
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
	srcRelayerWalletAmount := WalletAmount{
		Address: srcAccount,
		Denom:   srcChainCfg.Denom,
		Amount:  10000000,
	}

	// Fund relayer account on dst chain
	dstRelayerWalletAmount := WalletAmount{
		Address: dstAccount,
		Denom:   dstChainCfg.Denom,
		Amount:  10000000,
	}

	// Generate key to be used for "user" that will execute IBC transaction
	if err := srcChain.CreateKey(ctx, userAccountKeyName); err != nil {
		return errResponse(err)
	}

	srcUserAccountAddressBytes, err := srcChain.GetAddress(userAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	srcUserAccountSrc, err := types.Bech32ifyAddressBytes(srcChainCfg.Bech32Prefix, srcUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	srcUserAccountDst, err := types.Bech32ifyAddressBytes(dstChainCfg.Bech32Prefix, srcUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	if err := dstChain.CreateKey(ctx, userAccountKeyName); err != nil {
		return errResponse(err)
	}

	dstUserAccountAddressBytes, err := dstChain.GetAddress(userAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	dstUserAccountSrc, err := types.Bech32ifyAddressBytes(srcChainCfg.Bech32Prefix, dstUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	dstUserAccountDst, err := types.Bech32ifyAddressBytes(dstChainCfg.Bech32Prefix, dstUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	srcUser := User{
		KeyName:         userAccountKeyName,
		SrcChainAddress: srcUserAccountSrc,
		DstChainAddress: srcUserAccountDst,
	}

	dstUser := User{
		KeyName:         userAccountKeyName,
		SrcChainAddress: dstUserAccountSrc,
		DstChainAddress: dstUserAccountDst,
	}

	// Fund user account on src chain in order to relay from src to dst
	srcUserWalletAmount := WalletAmount{
		Address: srcUserAccountSrc,
		Denom:   srcChainCfg.Denom,
		Amount:  10000000000,
	}

	// Fund user account on dst chain in order to relay from dst to src
	dstUserWalletAmount := WalletAmount{
		Address: dstUserAccountDst,
		Denom:   dstChainCfg.Denom,
		Amount:  10000000000,
	}

	// start chains from genesis, wait until they are producing blocks
	chainsGenesisWaitGroup := errgroup.Group{}
	chainsGenesisWaitGroup.Go(func() error {
		return srcChain.Start(testName, ctx, []WalletAmount{srcRelayerWalletAmount, srcUserWalletAmount})
	})
	chainsGenesisWaitGroup.Go(func() error {
		return dstChain.Start(testName, ctx, []WalletAmount{dstRelayerWalletAmount, dstUserWalletAmount})
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
		if err := preRelayerStart(channels, srcUser, dstUser); err != nil {
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

	return relayerImpl, channels, &srcUser, &dstUser, relayerCleanup, nil
}

// StartICAChainsAndRelayer spins up chains containing the interchain accounts module and a relayer instance.
// It will create wallets in the relayer for src and dst chains, fund both wallets, create a user account on src chain
// and funds the user account on src chain in genesis.
func StartICAChainsAndRelayer(
	testName string,
	ctx context.Context,
	pool *dockertest.Pool,
	networkID string,
	home string,
	srcChain Chain,
	dstChain Chain,
	relayerImplementation RelayerImplementation,
	preRelayerStart func([]*ConnectionOutput, User, User) error,
) (Relayer, []*ConnectionOutput, *User, *User, func(), error) {
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
		// TODO not yet supported
	}

	errResponse := func(err error) (Relayer, []*ConnectionOutput, *User, *User, func(), error) {
		return nil, []*ConnectionOutput{}, nil, nil, nil, err
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
	srcRelayerWalletAmount := WalletAmount{
		Address: srcAccount,
		Denom:   srcChainCfg.Denom,
		Amount:  10000000,
	}

	// Fund relayer account on dst chain
	dstRelayerWalletAmount := WalletAmount{
		Address: dstAccount,
		Denom:   dstChainCfg.Denom,
		Amount:  10000000,
	}

	// Generate key to be used for "user" that will execute IBC transaction
	if err := srcChain.CreateKey(ctx, userAccountKeyName); err != nil {
		return errResponse(err)
	}

	srcUserAccountAddressBytes, err := srcChain.GetAddress(userAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	srcUserAccountSrc, err := types.Bech32ifyAddressBytes(srcChainCfg.Bech32Prefix, srcUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	srcUserAccountDst, err := types.Bech32ifyAddressBytes(dstChainCfg.Bech32Prefix, srcUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	if err := dstChain.CreateKey(ctx, userAccountKeyName); err != nil {
		return errResponse(err)
	}

	dstUserAccountAddressBytes, err := dstChain.GetAddress(userAccountKeyName)
	if err != nil {
		return errResponse(err)
	}

	dstUserAccountSrc, err := types.Bech32ifyAddressBytes(srcChainCfg.Bech32Prefix, dstUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	dstUserAccountDst, err := types.Bech32ifyAddressBytes(dstChainCfg.Bech32Prefix, dstUserAccountAddressBytes)
	if err != nil {
		return errResponse(err)
	}

	srcUser := User{
		KeyName:         userAccountKeyName,
		SrcChainAddress: srcUserAccountSrc,
		DstChainAddress: srcUserAccountDst,
	}

	dstUser := User{
		KeyName:         userAccountKeyName,
		SrcChainAddress: dstUserAccountSrc,
		DstChainAddress: dstUserAccountDst,
	}

	// Fund user account on src chain in order to relay from src to dst
	srcUserWalletAmount := WalletAmount{
		Address: srcUserAccountSrc,
		Denom:   srcChainCfg.Denom,
		Amount:  10000000000,
	}

	// Fund user account on dst chain in order to relay from dst to src
	dstUserWalletAmount := WalletAmount{
		Address: dstUserAccountDst,
		Denom:   dstChainCfg.Denom,
		Amount:  10000000000,
	}

	// start chains from genesis, wait until they are producing blocks
	chainsGenesisWaitGroup := errgroup.Group{}
	chainsGenesisWaitGroup.Go(func() error {
		return srcChain.Start(testName, ctx, []WalletAmount{srcRelayerWalletAmount, srcUserWalletAmount})
	})
	chainsGenesisWaitGroup.Go(func() error {
		return dstChain.Start(testName, ctx, []WalletAmount{dstRelayerWalletAmount, dstUserWalletAmount})
	})

	if err := chainsGenesisWaitGroup.Wait(); err != nil {
		return errResponse(err)
	}

	if err := relayerImpl.CreateClients(ctx, testPathName); err != nil {
		return errResponse(err)
	}

	time.Sleep(time.Second * 10)

	if err := relayerImpl.CreateConnections(ctx, testPathName); err != nil {
		return errResponse(err)
	}

	connections, err := relayerImpl.GetConnections(ctx, srcChainCfg.ChainID)
	if err != nil {
		return errResponse(err)
	}
	if len(connections) != 1 {
		return errResponse(fmt.Errorf("connection count invalid. expected: 1, actual: %d", len(connections)))
	}

	if preRelayerStart != nil {
		if err := preRelayerStart(connections, srcUser, dstUser); err != nil {
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

	return relayerImpl, connections, &srcUser, &dstUser, relayerCleanup, nil
}

func WaitForBlocks(srcChain Chain, dstChain Chain, blocksToWait int64) error {
	chainsConsecutiveBlocksWaitGroup := errgroup.Group{}
	chainsConsecutiveBlocksWaitGroup.Go(func() (err error) {
		_, err = srcChain.WaitForBlocks(blocksToWait)
		return
	})
	chainsConsecutiveBlocksWaitGroup.Go(func() (err error) {
		_, err = dstChain.WaitForBlocks(blocksToWait)
		return
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
		showContainerLogs := os.Getenv("SHOW_CONTAINER_LOGS")
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
					if showContainerLogs != "" {
						fmt.Printf("{%s} - stdout:\n%s\n{%s} - stderr:\n%s\n", names, stdout, names, stderr)
					}
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
