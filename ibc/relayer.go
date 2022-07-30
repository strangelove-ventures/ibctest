package ibc

import (
	"context"
	"fmt"
	"time"

	chantypes "github.com/cosmos/ibc-go/v4/modules/core/04-channel/types"
	ptypes "github.com/cosmos/ibc-go/v4/modules/core/05-port/types"
	host "github.com/cosmos/ibc-go/v4/modules/core/24-host"
	"github.com/strangelove-ventures/ibctest/internal/dockerutil"
)

// Relayer represents an instance of a relayer that can be support IBC.
// Built-in implementations are run through Docker,
// but they could exec out to external processes
// or even be implemented in-process in Go.
//
// All of the methods on Relayer accept a RelayerExecReporter.
// It is intended that Relayer implementations will call the reporters' TrackRelayerExec method
// so that details of the relayer execution are included in the test report.
//
// If a relayer does not properly call into the reporter,
// the tests will still execute properly,
// but the report will be missing details.
type Relayer interface {
	// restore a mnemonic to be used as a relayer wallet for a chain
	RestoreKey(ctx context.Context, rep RelayerExecReporter, chainID, keyName, mnemonic string) error

	// generate a new key
	AddKey(ctx context.Context, rep RelayerExecReporter, chainID, keyName string) (RelayerWallet, error)

	// GetWallet returns a RelayerWallet for that relayer on the given chain and a boolean indicating if it was found.
	GetWallet(chainID string) (RelayerWallet, bool)

	// add relayer configuration for a chain
	AddChainConfiguration(ctx context.Context, rep RelayerExecReporter, chainConfig ChainConfig, keyName, rpcAddr, grpcAddr string) error

	// generate new path between two chains
	GeneratePath(ctx context.Context, rep RelayerExecReporter, srcChainID, dstChainID, pathName string) error

	// setup channels, connections, and clients
	LinkPath(ctx context.Context, rep RelayerExecReporter, pathName string, opts CreateChannelOptions) error

	// update clients, such as after new genesis
	UpdateClients(ctx context.Context, rep RelayerExecReporter, pathName string) error

	// get channel IDs for chain
	GetChannels(ctx context.Context, rep RelayerExecReporter, chainID string) ([]ChannelOutput, error)

	// GetConnections returns a slice of IBC connection details composed of the details for each connection on a specified chain.
	GetConnections(ctx context.Context, rep RelayerExecReporter, chainID string) (ConnectionOutputs, error)

	// After configuration is initialized, begin relaying.
	// This method is intended to create a background worker that runs the relayer.
	// You must call StopRelayer to cleanly stop the relaying.
	StartRelayer(ctx context.Context, rep RelayerExecReporter, pathName string) error

	// StopRelayer stops a relayer that started work through StartRelayer.
	StopRelayer(ctx context.Context, rep RelayerExecReporter) error

	// FlushPackets flushes any outstanding packets and then returns.
	FlushPackets(ctx context.Context, rep RelayerExecReporter, pathName string, channelID string) error

	// FlushAcknowledgements flushes any outstanding acknowledgements and then returns.
	FlushAcknowledgements(ctx context.Context, rep RelayerExecReporter, pathName string, channelID string) error

	// CreateClients performs the client handshake steps necessary for creating a light client
	// on src that tracks the state of dst, and a light client on dst that tracks the state of src.
	CreateClients(ctx context.Context, rep RelayerExecReporter, pathName string) error

	// CreateConnections performs the connection handshake steps necessary for creating a connection
	// between the src and dst chains.
	CreateConnections(ctx context.Context, rep RelayerExecReporter, pathName string) error

	// CreateChannel creates a channel on the given path with the provided options.
	CreateChannel(ctx context.Context, rep RelayerExecReporter, pathName string, opts CreateChannelOptions) error

	// UseDockerNetwork reports whether the relayer is run in the same docker network as the other chains.
	//
	// If false, the relayer will connect to the localhost-exposed ports instead of the docker hosts.
	//
	// Relayer implementations provided by the ibctest module will report true,
	// but custom implementations may report false.
	UseDockerNetwork() bool

	// Exec runs an arbitrary command using Relayer's docker environment.
	// Whether the invoked command is run in a one-off container or execing into an already running container
	// is up to the relayer implementation.
	//
	// "env" are environment variables in the format "MY_ENV_VAR=value"
	Exec(ctx context.Context, rep RelayerExecReporter, cmd []string, env []string) dockerutil.ContainerExecResult

	// HomeDir is the home directory of a node running in a docker container. Therefore, this maps to
	// the container's filesystem (not the host).
	HomeDir() string
}

// CreateChannelOptions contains the configuration for creating a channel.
type CreateChannelOptions struct {
	SourcePortName string
	DestPortName   string

	Order Order

	Version string
}

// DefaultChannelOpts returns the default settings for creating an ics20 fungible token transfer channel.
func DefaultChannelOpts() CreateChannelOptions {
	return CreateChannelOptions{
		SourcePortName: "transfer",
		DestPortName:   "transfer",
		Order:          Unordered,
		Version:        "ics20-1",
	}
}

// Validate will check that the specified CreateChannelOptions are valid.
func (opts CreateChannelOptions) Validate() error {
	switch {
	case host.PortIdentifierValidator(opts.SourcePortName) != nil:
		return ptypes.ErrInvalidPort
	case host.PortIdentifierValidator(opts.DestPortName) != nil:
		return ptypes.ErrInvalidPort
	case opts.Version == "":
		return fmt.Errorf("invalid channel version")
	case opts.Order.Validate() != nil:
		return chantypes.ErrInvalidChannelOrdering
	}
	return nil
}

// Order represents an IBC channel's ordering.
type Order int

const (
	Invalid Order = iota
	Ordered
	Unordered
)

// String returns the lowercase string representation of the Order.
func (o Order) String() string {
	switch o {
	case Unordered:
		return "unordered"
	case Ordered:
		return "ordered"
	default:
		return "invalid"
	}
}

// Validate checks that the Order type is a valid value.
func (o Order) Validate() error {
	if o == Ordered || o == Unordered {
		return nil
	}
	return chantypes.ErrInvalidChannelOrdering
}

// ExecReporter is the interface of a narrow type returned by testreporter.RelayerExecReporter.
// This avoids a direct dependency on the testreporter package,
// and it avoids the relayer needing to be aware of a *testing.T.
type RelayerExecReporter interface {
	TrackRelayerExec(
		// The name of the docker container in which this relayer command executed,
		// or empty if it did not run in docker.
		containerName string,

		// The command line passed to this invocation of the relayer.
		command []string,

		// The standard output and standard error that the relayer produced during this invocation.
		stdout, stderr string,

		// The exit code of executing the command.
		// This field may not be applicable for e.g. an in-process relayer implementation.
		exitCode int,

		// When the command started and finished.
		startedAt, finishedAt time.Time,

		// Any error that occurred during execution.
		// This indicates a failure to execute,
		// e.g. the relayer binary not being found, failure communicating with Docker, etc.
		// If the process completed with a non-zero exit code,
		// those details should be indicated between stdout, stderr, and exitCode.
		err error,
	)
}

// NopRelayerExecReporter is a no-op RelayerExecReporter.
type NopRelayerExecReporter struct{}

func (NopRelayerExecReporter) TrackRelayerExec(string, []string, string, string, int, time.Time, time.Time, error) {
}
