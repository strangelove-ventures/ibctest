package ibctest

import (
	"fmt"
	"strings"

	"github.com/strangelove-ventures/ibctest/chain/cosmos"
	"github.com/strangelove-ventures/ibctest/chain/penumbra"
	"github.com/strangelove-ventures/ibctest/chain/polkadot"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/label"
	"go.uber.org/zap"
)

// ChainFactory describes how to get chains for tests.
// This type currently supports a Pair method,
// but it may be expanded to a Triplet method in the future.
type ChainFactory interface {
	// Count reports how many chains this factory will produce from its Chains method.
	Count() int

	// Chains returns a set of chains.
	Chains(testName string) ([]ibc.Chain, error)

	// Name returns a descriptive name of the factory,
	// indicating all of its chains.
	// Depending on how the factory was configured,
	// this may report more than two chains.
	Name() string

	// Labels are reported to allow simple filtering of tests depending on these Chains.
	// While the Name should be fully descriptive,
	// the Labels are intended to be short and fixed.
	Labels() []label.Chain
}

// BuiltinChainFactory implements ChainFactory to return a fixed set of chains.
// Use NewBuiltinChainFactory to create an instance.
type BuiltinChainFactory struct {
	log *zap.Logger

	specs []*ChainSpec
}

// builtinChainConfigs is a mapping of valid builtin chain names
// to their predefined ibc.ChainConfig.
var builtinChainConfigs = map[string]ibc.ChainConfig{
	"gaia":       cosmos.NewCosmosHeighlinerChainConfig("gaia", "gaiad", "cosmos", "uatom", "0.01uatom", 1.3, "504h", false),
	"osmosis":    cosmos.NewCosmosHeighlinerChainConfig("osmosis", "osmosisd", "osmo", "uosmo", "0.0uosmo", 1.3, "336h", false),
	"juno":       cosmos.NewCosmosHeighlinerChainConfig("juno", "junod", "juno", "ujuno", "0.0025ujuno", 1.3, "672h", false),
	"agoric":     cosmos.NewCosmosHeighlinerChainConfig("agoric", "agd", "agoric", "urun", "0.01urun", 1.3, "672h", true),
	"icad":       cosmos.NewCosmosHeighlinerChainConfig("icad", "icad", "cosmos", "photon", "0.00photon", 1.2, "504h", false),
	"penumbra":   penumbra.NewPenumbraChainConfig(),
	"composable": polkadot.NewComposableChainConfig(),
}

// NewBuiltinChainFactory returns a BuiltinChainFactory that returns chains defined by entries.
func NewBuiltinChainFactory(log *zap.Logger, specs []*ChainSpec) *BuiltinChainFactory {
	return &BuiltinChainFactory{log: log, specs: specs}
}

func (f *BuiltinChainFactory) Count() int {
	return len(f.specs)
}

func (f *BuiltinChainFactory) Chains(testName string) ([]ibc.Chain, error) {
	chains := make([]ibc.Chain, len(f.specs))
	for i, s := range f.specs {
		cfg, err := s.Config()
		if err != nil {
			// Prefer to wrap the error with the chain name if possible.
			if s.Name != "" {
				return nil, fmt.Errorf("failed to build chain config %s: %w", s.Name, err)
			}

			return nil, fmt.Errorf("failed to build chain config at index %d: %w", i, err)
		}

		chain, err := buildChain(f.log, testName, *cfg, s.NumValidators, s.NumFullNodes)
		if err != nil {
			return nil, err
		}
		chains[i] = chain
	}

	return chains, nil
}

const (
	defaultNumValidators = 2
	defaultNumFullNodes  = 1
)

func buildChain(log *zap.Logger, testName string, cfg ibc.ChainConfig, numValidators, numFullNodes *int) (ibc.Chain, error) {
	nv := defaultNumValidators
	if numValidators != nil {
		nv = *numValidators
	}
	nf := defaultNumFullNodes
	if numFullNodes != nil {
		nf = *numFullNodes
	}

	switch cfg.Type {
	case "cosmos":
		return cosmos.NewCosmosChain(testName, cfg, nv, nf, log), nil
	case "penumbra":
		return penumbra.NewPenumbraChain(log, testName, cfg, nv, nf), nil
	case "polkadot":
		switch {
		case strings.Contains(cfg.Name, "composable"):
			parachains := []polkadot.ParachainConfig{{
				Bin:             "composable",
				ChainID:         "dali-dev",
				Image:           cfg.Images[1],
				NumNodes:        nf,
				RelayChainFlags: []string{"--execution=wasm"},
			}}
			return polkadot.NewPolkadotChain(log, testName, cfg, nv, parachains), nil
		default:
			return nil, fmt.Errorf("unexpected error, unknown polkadot parachain: %s", cfg.Name)
		}
	default:
		return nil, fmt.Errorf("unexpected error, unknown chain type: %s for chain: %s", cfg.Type, cfg.Name)
	}
}

func (f *BuiltinChainFactory) Name() string {
	parts := make([]string, len(f.specs))
	for i, s := range f.specs {
		// Ignoring error here because if we fail to generate the config,
		// another part of the factory stack should have failed properly before we got here.
		cfg, _ := s.Config()

		v := s.Version
		if v == "" {
			v = cfg.Images[0].Version
		}

		parts[i] = cfg.Name + "@" + v
	}
	return strings.Join(parts, "+")
}

func (f *BuiltinChainFactory) Labels() []label.Chain {
	labels := make([]label.Chain, len(f.specs))
	for i, s := range f.specs {
		label := label.Chain(s.Name)
		if !label.IsKnown() {
			// The label must be known (i.e. registered),
			// otherwise filtering from the command line will be broken.
			panic(fmt.Errorf("chain name %s is not a known label", s.Name))
		}
		labels[i] = label
	}
	return labels
}
