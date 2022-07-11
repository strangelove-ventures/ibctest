package ibctest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/docker/docker/client"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/internal/blockdb"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// chainSet is an unordered collection of ibc.Chain,
// to group methods that apply actions against all chains in the set.
//
// The main purpose of the chainSet is to unify test setup when working with any number of chains.
type chainSet struct {
	log *zap.Logger

	chains map[ibc.Chain]struct{}

	// The following fields are set during TrackBlocks, and used in Close.
	trackerEg  *errgroup.Group
	db         *sql.DB
	collectors []*blockdb.Collector
}

func newChainSet(log *zap.Logger, chains []ibc.Chain) *chainSet {
	cs := &chainSet{
		log: log,

		chains: make(map[ibc.Chain]struct{}, len(chains)),
	}

	for _, chain := range chains {
		cs.chains[chain] = struct{}{}
	}

	return cs
}

// Initialize concurrently calls Initialize against each chain in the set.
// Each chain may run a docker pull command,
// so with a cold image cache, running concurrently may save some time.
func (cs *chainSet) Initialize(testName string, homeDir string, cli *client.Client, networkID string, opts ...ibc.ChainOption) error {
	var eg errgroup.Group

	for c := range cs.chains {
		c := c
		eg.Go(func() error {
			if err := c.Initialize(testName, homeDir, cli, networkID, opts...); err != nil {
				return fmt.Errorf("failed to initialize chain %s: %w", c.Config().Name, err)
			}

			return nil
		})
	}

	return eg.Wait()
}

// CreateCommonAccount creates a key with the given name on each chain in the set,
// and returns the bech32 representation of each account created.
// The typical use of CreateCommonAccount is to create a faucet account on each chain.
//
// The keys are created concurrently because creating keys on one chain
// should have no effect on any other chain.
func (cs *chainSet) CreateCommonAccount(ctx context.Context, keyName string) (bech32 map[ibc.Chain]string, err error) {
	var mu sync.Mutex
	bech32 = make(map[ibc.Chain]string, len(cs.chains))

	eg, egCtx := errgroup.WithContext(ctx)

	for c := range cs.chains {
		c := c
		eg.Go(func() error {
			config := c.Config()

			if err := c.CreateKey(egCtx, keyName); err != nil {
				return fmt.Errorf("failed to create key with name %q on chain %s: %w", keyName, config.Name, err)
			}

			addrBytes, err := c.GetAddress(egCtx, keyName)
			if err != nil {
				return fmt.Errorf("failed to get account address for key %q on chain %s: %w", keyName, config.Name, err)
			}

			b32, err := types.Bech32ifyAddressBytes(config.Bech32Prefix, addrBytes)
			if err != nil {
				return fmt.Errorf("failed to Bech32ifyAddressBytes on chain %s: %w", config.Name, err)
			}

			mu.Lock()
			bech32[c] = b32
			mu.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to create common account with name %s: %w", keyName, err)
	}

	return bech32, nil
}

// Start concurrently calls Start against each chain in the set.
func (cs *chainSet) Start(ctx context.Context, testName string, additionalGenesisWallets map[ibc.Chain][]ibc.WalletAmount) error {
	eg, egCtx := errgroup.WithContext(ctx)

	for c := range cs.chains {
		c := c
		eg.Go(func() error {
			if err := c.Start(testName, egCtx, additionalGenesisWallets[c]...); err != nil {
				return fmt.Errorf("failed to start chain %s: %w", c.Config().Name, err)
			}

			return nil
		})
	}

	return eg.Wait()
}

// TrackBlocks initializes database tables and polls for transactions to be saved in the database.
// This method is a nop if dbPath is blank.
// The gitSha is used to pin a git commit to a test invocation. Thus, when a user is looking at historical
// data they are able to determine which version of the code produced the results.
// Expected to be called after Start.
func (cs chainSet) TrackBlocks(ctx context.Context, testName, dbPath, gitSha string) error {
	if len(dbPath) == 0 {
		// nop
		return nil
	}

	db, err := blockdb.ConnectDB(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("connect to sqlite database %s: %w", dbPath, err)
	}
	cs.db = db

	if len(gitSha) == 0 {
		gitSha = "unknown"
	}

	if err := blockdb.Migrate(db, gitSha); err != nil {
		return fmt.Errorf("migrate sqlite database %s; deleting file recommended: %w", dbPath, err)
	}

	testCase, err := blockdb.CreateTestCase(ctx, db, testName, gitSha)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("create test case in sqlite database: %w", err)
	}

	// TODO (nix - 6/1/22) Need logger instead of fmt.Fprint
	cs.trackerEg = new(errgroup.Group)
	cs.collectors = make([]*blockdb.Collector, len(cs.chains))
	i := 0
	for c := range cs.chains {
		c := c
		id := c.Config().ChainID
		finder, ok := c.(blockdb.TxFinder)
		if !ok {
			fmt.Fprintf(os.Stderr, `Chain %s is not configured to save blocks; must implement "FindTxs(ctx context.Context, height uint64) ([][]byte, error)"`+"\n", id)
			return nil
		}
		j := i // Avoid closure on loop variable.
		cs.trackerEg.Go(func() error {
			chaindb, err := testCase.AddChain(ctx, id, c.Config().Type)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to add chain %s to database: %v", id, err)
				return nil
			}
			log := cs.log.With(zap.String("chain_id", id))
			collector := blockdb.NewCollector(log, finder, chaindb, 100*time.Millisecond)
			cs.collectors[j] = collector
			collector.Collect(ctx)
			return nil
		})
		i++
	}

	return nil
}

// Close frees any resources associated with the chainSet.
//
// Currently, it only frees resources from TrackBlocks.
// Close is safe to call even if TrackBlocks was not called.
func (cs *chainSet) Close() error {
	for _, c := range cs.collectors {
		if c != nil {
			c.Stop()
		}
	}

	var err error
	if cs.trackerEg != nil {
		multierr.AppendInto(&err, cs.trackerEg.Wait())
	}
	if cs.db != nil {
		multierr.AppendInto(&err, cs.db.Close())
	}
	return err
}
