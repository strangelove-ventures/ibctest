// Command ibctest allows running the relayer tests with command-line configuration.
package ibctest

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/strangelove-ventures/ibctest"
	"github.com/strangelove-ventures/ibctest/conformance"
	"github.com/strangelove-ventures/ibctest/ibc"
	"github.com/strangelove-ventures/ibctest/internal/blockdb"
	blockdbtui "github.com/strangelove-ventures/ibctest/internal/blockdb/tui"
	"github.com/strangelove-ventures/ibctest/internal/version"
	"github.com/strangelove-ventures/ibctest/testreporter"
	"go.uber.org/zap"
)

func init() {
	// Because we use the test binary, we use this hack to customize the help usage.
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprint(out, `Subcommands:

  debug  Open UI to debug blocks and transactions.
`)
		debugFlagSet.PrintDefaults()
		fmt.Fprint(out, `
  version  Prints git commit that produced executable.
`)
	}
}

// The value of the test matrix.
var testMatrix struct {
	Relayers []string

	ChainSets [][]*ibctest.ChainSpec
}

var debugFlagSet = flag.NewFlagSet("debug", flag.ExitOnError)

func TestMain(m *testing.M) {
	addFlags()
	parseFlags()

	ctx := context.Background()

	switch subcommand() {
	case "debug":
		if err := runDebugTerminalUI(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to run debug: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "version":
		fmt.Fprintln(os.Stderr, version.GitSha)
		os.Exit(0)
	}

	if err := setUpTestMatrix(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build test matrix: %v\n", err)
		os.Exit(1)
	}

	if err := validateTestMatrix(); err != nil {
		fmt.Fprintf(os.Stderr, "Test matrix invalid: %v\n", err)
		os.Exit(1)
	}

	if err := configureTestReporter(); err != nil {
		fmt.Fprintf(os.Stderr, "Failure configuring test reporter: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := reporter.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Failure closing test reporter: %v\n", err)
		// Don't os.Exit here, since we already have an exit code from running the tests.
	}

	os.Exit(code)
}

var extraFlags mainFlags

// setUpTestMatrix populates the testMatrix singleton with
// the parsed contents of the file referenced by the matrix flag,
// or with a small reasonable default of rly against one gaia-osmosis set.
func setUpTestMatrix() error {
	if extraFlags.MatrixFile == "" {
		fmt.Fprintln(os.Stderr, "No matrix file provided, falling back to rly with gaia and osmosis")

		testMatrix.Relayers = []string{"rly"}
		testMatrix.ChainSets = [][]*ibctest.ChainSpec{
			{
				{Name: "gaia", Version: "v7.0.1"},
				{Name: "osmosis", Version: "v7.2.0"},
			},
		}

		return nil
	}

	// Otherwise parse the given file.
	fmt.Fprintf(os.Stderr, "Loading matrix file from %s\n", extraFlags.MatrixFile)
	j, err := os.ReadFile(extraFlags.MatrixFile)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(j, &testMatrix); err != nil {
		return err
	}

	return nil
}

func validateTestMatrix() error {
	nop := zap.NewNop()
	for _, r := range testMatrix.Relayers {
		if _, err := getRelayerFactory(r, nop); err != nil {
			return err
		}
	}

	for _, cs := range testMatrix.ChainSets {
		if _, err := getChainFactory(nop, cs); err != nil {
			return err
		}
	}

	return nil
}

var reporter *testreporter.Reporter

func configureTestReporter() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home dir: %w", err)
	}
	fpath := filepath.Join(home, ".ibctest", "reports")
	err = os.MkdirAll(fpath, 0755)
	if err != nil {
		return fmt.Errorf("mkdirall: %w", err)
	}

	f, err := os.Create(filepath.Join(fpath, fmt.Sprintf("%d.json", time.Now().Unix())))
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Writing report to %s\n", f.Name())

	reporter = testreporter.NewReporter(f)
	return nil
}

func getRelayerFactory(name string, logger *zap.Logger) (ibctest.RelayerFactory, error) {
	switch name {
	case "rly", "cosmos/relayer":
		return ibctest.NewBuiltinRelayerFactory(ibc.CosmosRly, logger), nil
	case "hermes":
		return ibctest.NewBuiltinRelayerFactory(ibc.Hermes, logger), nil
	default:
		return nil, fmt.Errorf("unknown relayer type %q (valid types: rly, hermes)", name)
	}
}

func getChainFactory(log *zap.Logger, chainSpecs []*ibctest.ChainSpec) (ibctest.ChainFactory, error) {
	if len(chainSpecs) != 2 {
		return nil, fmt.Errorf("chain specs must have length 2 (found a chain set of length %d)", len(chainSpecs))
	}
	return ibctest.NewBuiltinChainFactory(log, chainSpecs), nil
}

// TestConformance is the root test for the ibc conformance tests.
// It runs many subtests in parallel;
// if this is too taxing on a system, the -test.parallel flag
// can be used to reduce how many tests actively run at once.
func TestConformance(t *testing.T) {
	t.Parallel()

	logger, err := extraFlags.Logger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	t.Logf("View chain and relayer logs at %s", logger.FilePath)

	log := logger.Logger

	// Build a set of chain factories from the provided chain sets.
	chainFactories := make([]ibctest.ChainFactory, 0, len(testMatrix.ChainSets))
	for _, cs := range testMatrix.ChainSets {
		cf, err := getChainFactory(log, cs)
		if err != nil {
			// This error should have been validated before running tests.
			panic(err)
		}
		chainFactories = append(chainFactories, cf)
	}

	// Materialize all the relayer factories.
	relayerFactories := make([]ibctest.RelayerFactory, len(testMatrix.Relayers))
	for i, r := range testMatrix.Relayers {
		rf, err := getRelayerFactory(r, log)
		if err != nil {
			// This error should have been validated before running tests.
			panic(err)
		}

		relayerFactories[i] = rf
	}

	// Begin test execution, which will spawn many parallel subtests.
	conformance.Test(t, chainFactories, relayerFactories, reporter)
}

// addFlags configures additional flags beyond the default testing flags.
// Although pflag would have been slightly more developer friendly,
// I ran out of time to spend on getting pflag to cooperate with the
// testing flags, so I fell back to plain Go standard library flags.
// We can revisit if necessary.
func addFlags() {
	flag.StringVar(&extraFlags.MatrixFile, "matrix", "", "Path to matrix file defining what configurations to test")
	flag.StringVar(&extraFlags.LogFile, "log-file", "ibctest.log", "File to write chain and relayer logs. If a file name, logs written to $HOME/.ibctest/logs directory. Use 'stderr' or 'stdout' to print logs in line tests.")
	flag.StringVar(&extraFlags.LogFormat, "log-format", "console", "Chain and relayer log format: console|json")
	flag.StringVar(&extraFlags.LogLevel, "log-level", "info", "Chain and relayer log level: debug|info|error")
	flag.StringVar(&extraFlags.ReportFile, "report-file", "", "Path where test report will be stored. Defaults to $HOME/.ibctest/reports/$TIMESTAMP.json")

	debugFlagSet.StringVar(&extraFlags.BlockDatabaseFile, "block-db", ibctest.DefaultBlockDatabaseFilepath(), "Path to database sqlite file that tracks blocks and transactions.")
}

func parseFlags() {
	flag.Parse()
	switch subcommand() {
	case "debug":
		// Ignore errors because configured with flag.ExitOnError.
		_ = debugFlagSet.Parse(os.Args[2:])
	}
}

func subcommand() string {
	return flag.Arg(0)
}

func runDebugTerminalUI(ctx context.Context) error {
	dbPath := extraFlags.BlockDatabaseFile

	// Explicitly check for file existence otherwise blockdb.ConnectDB implicitly creates and migrates a sqlite file.
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}

	db, err := blockdb.ConnectDB(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("connect to database %s: %w", dbPath, err)
	}
	defer db.Close()

	if err = blockdb.Migrate(db, version.GitSha); err != nil {
		return fmt.Errorf("migrate database %s: %w", dbPath, err)
	}

	querySvc := blockdb.NewQuery(db)

	schemaInfo, err := querySvc.CurrentSchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("query schema version: %w", err)
	}

	testCases, err := querySvc.RecentTestCases(ctx, 100)
	if err != nil {
		return fmt.Errorf("query recent test cases: %w", err)
	}
	if len(testCases) == 0 {
		return fmt.Errorf("no test cases found in database %s", dbPath)
	}

	app := tview.NewApplication()
	model := blockdbtui.NewModel(blockdb.NewQuery(db), dbPath, schemaInfo.GitSha, schemaInfo.CreatedAt, testCases)
	return app.
		SetInputCapture(model.Update(ctx)).
		SetRoot(model.RootView(), true).
		Run()
}
