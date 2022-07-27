package penumbra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/internal/dockerutil"
	"go.uber.org/zap"
)

type PenumbraAppNode struct {
	log *zap.Logger

	Index        int
	VolumeName   string
	Chain        ibc.Chain
	TestName     string
	NetworkID    string
	DockerClient *client.Client
	Image        ibc.DockerImage

	containerID string

	// Set during StartContainer.
	hostRPCPort  string
	hostGRPCPort string
}

const (
	valKey         = "validator"
	rpcPort        = "26657/tcp"
	tendermintPort = "26658/tcp"
	grpcPort       = "9090/tcp"
)

var exposedPorts = nat.PortSet{
	nat.Port(tendermintPort): {},
}

// Name of the test node container
func (p *PenumbraAppNode) Name() string {
	return fmt.Sprintf("pd-%d-%s-%s", p.Index, p.Chain.Config().ChainID, p.TestName)
}

// the hostname of the test node container
func (p *PenumbraAppNode) HostName() string {
	return dockerutil.CondenseHostName(p.Name())
}

// Bind returns the home folder bind point for running the node
func (p *PenumbraAppNode) Bind() []string {
	return []string{fmt.Sprintf("%s:%s", p.VolumeName, p.HomeDir())}
}

func (p *PenumbraAppNode) HomeDir() string {
	return fmt.Sprintf("/root/.%s", p.Chain.Config().Name)
}

func (p *PenumbraAppNode) CreateKey(ctx context.Context, keyName string) error {
	cmd := []string{"pcli", "-w", p.WalletPathContainer(), "wallet", "generate"}
	_, stderr, err := p.Exec(ctx, cmd, nil, dockerutil.LogTailAll)
	// already exists error is okay
	if err != nil && !strings.Contains(string(stderr), "already exists, refusing to overwrite it") {
		return err
	}
	cmd = []string{"pcli", "-w", p.WalletPathContainer(), "addr", "new", keyName}
	_, _, err = p.Exec(ctx, cmd, nil, dockerutil.LogTailDefault)
	return err
}

// initializes validator definition template file
// wallet must be generated first
func (p *PenumbraAppNode) InitValidatorFile(ctx context.Context) error {
	cmd := []string{
		"pcli",
		"-w", p.WalletPathContainer(),
		"validator", "template-definition",
		"--file", p.ValidatorDefinitionTemplateFilePathContainer(),
	}
	_, _, err := p.Exec(ctx, cmd, nil, dockerutil.LogTailDefault)
	return err
}

func (p *PenumbraAppNode) ValidatorDefinitionTemplateFilePathContainer() string {
	return filepath.Join(p.HomeDir(), "validator.json")
}

func (p *PenumbraAppNode) WalletPathContainer() string {
	return filepath.Join(p.HomeDir(), "wallet")
}

func (p *PenumbraAppNode) ValidatorsInputFileContainer() string {
	return filepath.Join(p.HomeDir(), "validators.json")
}

func (p *PenumbraAppNode) AllocationsInputFileContainer() string {
	return filepath.Join(p.HomeDir(), "allocations.csv")
}

func (p *PenumbraAppNode) genesisFileContent(ctx context.Context) ([]byte, error) {
	fr := dockerutil.NewFileRetriever(p.log, p.DockerClient, p.TestName)
	gen, err := fr.SingleFileContent(ctx, p.VolumeName, "node0/tendermint/config/genesis.json")
	if err != nil {
		return nil, fmt.Errorf("getting genesis.json content: %w", err)
	}

	return gen, nil
}

func (p *PenumbraAppNode) GenerateGenesisFile(
	ctx context.Context,
	chainID string,
	validators []PenumbraValidatorDefinition,
	allocations []PenumbraGenesisAppStateAllocation,
) error {
	validatorsJson, err := json.Marshal(validators)
	if err != nil {
		return fmt.Errorf("error marshalling validators to json: %w", err)
	}
	fw := dockerutil.NewFileWriter(p.log, p.DockerClient, p.TestName)
	if err := fw.WriteFile(ctx, p.VolumeName, "validators.json", validatorsJson); err != nil {
		return fmt.Errorf("error writing validators to file: %w", err)
	}
	allocationsCsv := []byte(`"amount","denom","address"\n`)
	for _, allocation := range allocations {
		allocationsCsv = append(allocationsCsv, []byte(fmt.Sprintf(`"%d","%s","%s"\n`, allocation.Amount, allocation.Denom, allocation.Address))...)
	}
	if err := fw.WriteFile(ctx, p.VolumeName, "allocations.csv", allocationsCsv); err != nil {
		return fmt.Errorf("error writing allocations to file: %w", err)
	}
	cmd := []string{
		"pd",
		"generate-testnet",
		"--chain-id", chainID,
		"--validators-input-file", p.ValidatorsInputFileContainer(),
		"--allocations-input-file", p.AllocationsInputFileContainer(),
		"--output-dir", p.HomeDir(),
	}
	_, _, err = p.Exec(ctx, cmd, nil, dockerutil.LogTailDefault)
	return err
}

func (p *PenumbraAppNode) GetAddress(ctx context.Context, keyName string) ([]byte, error) {
	cmd := []string{"pcli", "-w", p.WalletPathContainer(), "addr", "list"}
	stdout, _, err := p.Exec(ctx, cmd, nil, dockerutil.LogTailAll)
	if err != nil {
		return nil, err
	}
	addresses := strings.Split(string(stdout), "\n")
	for _, address := range addresses {
		fields := strings.Fields(address)
		if len(fields) < 3 {
			continue
		}
		if fields[1] == keyName {
			// TODO penumbra address is bech32m. need to decode to bytes here
			return []byte(fields[2]), nil
		}
	}
	return []byte{}, errors.New("address not found")
}

func (p *PenumbraAppNode) GetAddressBech32m(ctx context.Context, keyName string) (string, error) {
	cmd := []string{"pcli", "-w", p.WalletPathContainer(), "addr", "list"}
	stdout, _, err := p.Exec(ctx, cmd, nil, dockerutil.LogTailAll)
	if err != nil {
		return "", err
	}
	addresses := strings.Split(string(stdout), "\n")
	for _, address := range addresses {
		fields := strings.Fields(address)
		if len(fields) < 3 {
			continue
		}
		if fields[1] == keyName {
			return fields[2], nil
		}
	}
	return "", errors.New("address not found")
}

func (p *PenumbraAppNode) SendFunds(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	return errors.New("not yet implemented")
}

func (p *PenumbraAppNode) SendIBCTransfer(ctx context.Context, channelID, keyName string, amount ibc.WalletAmount, timeout *ibc.IBCTimeout) (ibc.Tx, error) {
	return ibc.Tx{}, errors.New("not yet implemented")
}

func (p *PenumbraAppNode) CreateNodeContainer(ctx context.Context) error {
	cmd := []string{"pd", "start", "--host", "0.0.0.0", "-r", p.HomeDir()}
	fmt.Printf("{%s} -> '%s'\n", p.Name(), strings.Join(cmd, " "))

	cc, err := p.DockerClient.ContainerCreate(
		ctx,
		&container.Config{
			Image: p.Image.Ref(),

			Entrypoint: []string{},
			Cmd:        cmd,

			Hostname: p.HostName(),
			User:     dockerutil.GetRootUserString(),

			Labels: map[string]string{dockerutil.CleanupLabel: p.TestName},

			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			Binds:           p.Bind(),
			PublishAllPorts: true,
			AutoRemove:      false,
			DNS:             []string{},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				p.NetworkID: {},
			},
		},
		nil,
		p.Name(),
	)
	if err != nil {
		return err
	}
	p.containerID = cc.ID
	return nil
}

func (p *PenumbraAppNode) StopContainer(ctx context.Context) error {
	timeout := 30 * time.Second
	return p.DockerClient.ContainerStop(ctx, p.containerID, &timeout)
}

func (p *PenumbraAppNode) StartContainer(ctx context.Context) error {
	if err := dockerutil.StartContainer(ctx, p.DockerClient, p.containerID); err != nil {
		return err
	}

	c, err := p.DockerClient.ContainerInspect(ctx, p.containerID)
	if err != nil {
		return err
	}

	p.hostRPCPort = dockerutil.GetHostPort(c, rpcPort)
	p.hostGRPCPort = dockerutil.GetHostPort(c, grpcPort)

	return nil
}

// Exec run a container for a specific job and block until the container exits
func (p *PenumbraAppNode) Exec(ctx context.Context, cmd []string, env []string, tail uint64) ([]byte, []byte, error) {
	job := dockerutil.NewImage(p.log, p.DockerClient, p.NetworkID, p.TestName, p.Image.Repository, p.Image.Version)
	opts := dockerutil.ContainerOptions{
		Binds: p.Bind(),
		Env:   env,
		User:  dockerutil.GetRootUserString(),
		Tail:  tail,
	}
	return job.Run(ctx, cmd, opts)
}
