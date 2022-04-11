package ibc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
)

type CosmosRelayer struct {
	src       Chain
	dst       Chain
	pool      *dockertest.Pool
	container *docker.Container
	networkID string
	home      string
	testName  string
}

type CosmosRelayerChainConfigValue struct {
	Key            string  `json:"key"`
	ChainID        string  `json:"chain-id"`
	RPCAddr        string  `json:"rpc-addr"`
	GRPCAddr       string  `json:"grpc-addr"`
	AccountPrefix  string  `json:"account-prefix"`
	KeyringBackend string  `json:"keyring-backend"`
	GasAdjustment  float64 `json:"gas-adjustment"`
	GasPrices      string  `json:"gas-prices"`
	Debug          bool    `json:"debug"`
	Timeout        string  `json:"timeout"`
	OutputFormat   string  `json:"output-format"`
	SignMode       string  `json:"sign-mode"`
}

type CosmosRelayerChainConfig struct {
	Type  string                        `json:"type"`
	Value CosmosRelayerChainConfigValue `json:"value"`
}

var (
	containerImage   = "ghcr.io/cosmos/relayer"
	containerVersion = "latest"
)

func ChainConfigToCosmosRelayerChainConfig(chainConfig ChainConfig, keyName, rpcAddr, gprcAddr string) CosmosRelayerChainConfig {
	return CosmosRelayerChainConfig{
		Type: chainConfig.Type,
		Value: CosmosRelayerChainConfigValue{
			Key:            keyName,
			ChainID:        chainConfig.ChainID,
			RPCAddr:        rpcAddr,
			GRPCAddr:       gprcAddr,
			AccountPrefix:  chainConfig.Bech32Prefix,
			KeyringBackend: keyring.BackendTest,
			GasAdjustment:  chainConfig.GasAdjustment,
			GasPrices:      chainConfig.GasPrices,
			Debug:          true,
			Timeout:        "10s",
			OutputFormat:   "json",
			SignMode:       "direct",
		},
	}
}

func NewCosmosRelayerFromChains(testName string, src, dst Chain, pool *dockertest.Pool, networkID string, home string) *CosmosRelayer {
	relayer := &CosmosRelayer{
		src:       src,
		dst:       dst,
		pool:      pool,
		networkID: networkID,
		home:      home,
		testName:  testName,
	}
	relayer.MkDir()

	return relayer
}

func (relayer *CosmosRelayer) Name() string {
	return fmt.Sprintf("rly-%s", relayer.testName)
}

func (relayer *CosmosRelayer) LinkPath(ctx context.Context, pathName string) error {
	command := []string{"rly", "tx", "link", pathName,
		"--home", relayer.NodeHome(),
	}
	return handleNodeJobError(relayer.NodeJob(ctx, command))
}

func (relayer *CosmosRelayer) GetChannels(ctx context.Context, chainID string) ([]ChannelOutput, error) {
	command := []string{"rly", "q", "channels", chainID,
		"--home", relayer.NodeHome(),
	}
	exitCode, stdout, stderr, err := relayer.NodeJob(ctx, command)
	if err != nil {
		return []ChannelOutput{}, handleNodeJobError(exitCode, stdout, stderr, err)
	}
	channels := []ChannelOutput{}
	channelSplit := strings.Split(stdout, "\n")
	for _, channel := range channelSplit {
		if strings.TrimSpace(channel) == "" {
			continue
		}
		channelOutput := ChannelOutput{}
		err := json.Unmarshal([]byte(channel), &channelOutput)
		if err != nil {
			fmt.Printf("error parsing channels json: %v\n", err)
			continue
		}
		channels = append(channels, channelOutput)
	}

	return channels, handleNodeJobError(exitCode, stdout, stderr, err)
}

// Implements Relayer interface
func (relayer *CosmosRelayer) StartRelayer(ctx context.Context, pathName string) error {
	return relayer.CreateNodeContainer(pathName)
}

// Implements Relayer interface
func (relayer *CosmosRelayer) StopRelayer(ctx context.Context) error {
	return relayer.StopContainer()
}

// Implements Relayer interface
func (relayer *CosmosRelayer) ClearQueue(ctx context.Context, pathName string, channelID string) error {
	command := []string{"rly", "tx", "relay-pkts", pathName, channelID, "--home", relayer.NodeHome()}
	return handleNodeJobError(relayer.NodeJob(ctx, command))
}

// Implements Relayer interface
func (relayer *CosmosRelayer) AddChainConfiguration(ctx context.Context, chainConfig ChainConfig, keyName, rpcAddr, grpcAddr string) error {

	if _, err := os.Stat(fmt.Sprintf("%s/config", relayer.Dir())); os.IsNotExist(err) {
		command := []string{"rly", "config", "init",
			"--home", relayer.NodeHome(),
		}
		exitCode, stdout, stderr, err := relayer.NodeJob(ctx, command)
		if err != nil {
			return handleNodeJobError(exitCode, stdout, stderr, err)
		}
	}

	chainConfigFile := fmt.Sprintf("%s.json", chainConfig.ChainID)

	chainConfigLocalFilePath := fmt.Sprintf("%s/%s", relayer.Dir(), chainConfigFile)
	chainConfigContainerFilePath := fmt.Sprintf("%s/%s", relayer.NodeHome(), chainConfigFile)

	cosmosRelayerChainConfig := ChainConfigToCosmosRelayerChainConfig(chainConfig, keyName, rpcAddr, grpcAddr)
	json, err := json.Marshal(cosmosRelayerChainConfig)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(chainConfigLocalFilePath, json, 0644) //nolint
	if err != nil {
		return err
	}

	command := []string{"rly", "chains", "add", "-f", chainConfigContainerFilePath,
		"--home", relayer.NodeHome(),
	}
	return handleNodeJobError(relayer.NodeJob(ctx, command))
}

// Implements Relayer interface
func (relayer *CosmosRelayer) GeneratePath(ctx context.Context, srcChainID, dstChainID, pathName string) error {
	command := []string{"rly", "paths", "new", srcChainID, dstChainID, pathName,
		"--home", relayer.NodeHome(),
	}
	return handleNodeJobError(relayer.NodeJob(ctx, command))
}

func (relayer *CosmosRelayer) CreateNodeContainer(pathName string) error {
	err := relayer.pool.Client.PullImage(docker.PullImageOptions{
		Repository: containerImage,
		Tag:        containerVersion,
	}, docker.AuthConfiguration{})
	if err != nil {
		return err
	}
	containerName := fmt.Sprintf("%s-%s", relayer.Name(), pathName)
	cmd := []string{"rly", "start", pathName, "--home", relayer.NodeHome()}
	fmt.Printf("{%s} -> '%s'\n", containerName, strings.Join(cmd, " "))
	cont, err := relayer.pool.Client.CreateContainer(docker.CreateContainerOptions{
		Name: containerName,
		Config: &docker.Config{
			User:       getDockerUserString(),
			Cmd:        cmd,
			Entrypoint: []string{},
			Hostname:   containerName,
			Image:      fmt.Sprintf("%s:%s", containerImage, containerVersion),
			Labels:     map[string]string{"ibc-test": relayer.testName},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointConfig{
				relayer.networkID: {},
			},
		},
		HostConfig: &docker.HostConfig{
			Binds:      relayer.Bind(),
			AutoRemove: false,
		},
	})
	if err != nil {
		return err
	}
	relayer.container = cont
	if err := relayer.pool.Client.StartContainer(relayer.container.ID, nil); err != nil {
		return err
	}
	return nil
}

// NodeJob run a container for a specific job and block until the container exits
// NOTE: on job containers generate random name
func (relayer *CosmosRelayer) NodeJob(ctx context.Context, cmd []string) (int, string, string, error) {
	err := relayer.pool.Client.PullImage(docker.PullImageOptions{
		Repository: containerImage,
		Tag:        containerVersion,
	}, docker.AuthConfiguration{})
	if err != nil {
		return 1, "", "", err
	}
	counter, _, _, _ := runtime.Caller(1)
	caller := runtime.FuncForPC(counter).Name()
	funcName := strings.Split(caller, ".")
	container := fmt.Sprintf("%s-%s-%s", relayer.Name(), funcName[len(funcName)-1], RandLowerCaseLetterString(3))
	fmt.Printf("{%s} -> '%s'\n", container, strings.Join(cmd, " "))
	cont, err := relayer.pool.Client.CreateContainer(docker.CreateContainerOptions{
		Name: container,
		Config: &docker.Config{
			User:       getDockerUserString(),
			Hostname:   container,
			Image:      fmt.Sprintf("%s:%s", containerImage, containerVersion),
			Cmd:        cmd,
			Entrypoint: []string{},
			Labels:     map[string]string{"ibc-test": container},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointConfig{
				relayer.networkID: {},
			},
		},
		HostConfig: &docker.HostConfig{
			Binds:      relayer.Bind(),
			AutoRemove: false,
		},
	})
	if err != nil {
		return 1, "", "", err
	}
	if err := relayer.pool.Client.StartContainer(cont.ID, nil); err != nil {
		return 1, "", "", err
	}
	exitCode, err := relayer.pool.Client.WaitContainerWithContext(cont.ID, ctx)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	_ = relayer.pool.Client.Logs(docker.LogsOptions{Context: ctx, Container: cont.ID, OutputStream: stdout, ErrorStream: stderr, Stdout: true, Stderr: true, Tail: "100", Follow: false, Timestamps: false})
	_ = relayer.pool.Client.RemoveContainer(docker.RemoveContainerOptions{ID: cont.ID})
	fmt.Printf("{%s} - stdout:\n%s\n{%s} - stderr:\n%s\n", container, stdout.String(), container, stderr.String())
	return exitCode, stdout.String(), stderr.String(), err
}

// CreateKey creates a key in the keyring backend test for the given node
func (relayer *CosmosRelayer) RestoreKey(ctx context.Context, chainID, keyName, mnemonic string) error {
	command := []string{"rly", "keys", "restore", chainID, keyName, mnemonic,
		"--home", relayer.NodeHome(),
	}
	return handleNodeJobError(relayer.NodeJob(ctx, command))
}

func (relayer *CosmosRelayer) AddKey(ctx context.Context, chainID, keyName string) (RelayerWallet, error) {
	command := []string{"rly", "keys", "add", chainID, keyName,
		"--home", relayer.NodeHome(),
	}
	exitCode, stdout, stderr, err := relayer.NodeJob(ctx, command)
	wallet := RelayerWallet{}
	if err != nil {
		return wallet, handleNodeJobError(exitCode, stdout, stderr, err)
	}
	err = json.Unmarshal([]byte(stdout), &wallet)
	if err != nil {
		return wallet, err
	}

	return wallet, nil
}

// Dir is the directory where the test node files are stored
func (relayer *CosmosRelayer) Dir() string {
	return fmt.Sprintf("%s/%s/", relayer.home, relayer.Name())
}

// MkDir creates the directory for the testnode
func (relayer *CosmosRelayer) MkDir() {
	if err := os.MkdirAll(relayer.Dir(), 0755); err != nil {
		panic(err)
	}
}

func (relayer *CosmosRelayer) NodeHome() string {
	return "/tmp/relayer"
}

// Bind returns the home folder bind point for running the node
func (relayer *CosmosRelayer) Bind() []string {
	return []string{fmt.Sprintf("%s:%s", relayer.Dir(), relayer.NodeHome())}
}

func (relayer *CosmosRelayer) StopContainer() error {
	return relayer.pool.Client.StopContainer(relayer.container.ID, uint(time.Second*30))
}
