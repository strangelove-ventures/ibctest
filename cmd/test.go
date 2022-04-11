/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>

*/
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/strangelove-ventures/ibc-test-framework/ibc"
	"golang.org/x/sync/errgroup"
)

func parseChainVersion(chainVersion string) (string, string) {
	split := strings.Split(chainVersion, ":")
	switch len(split) {
	case 1:
		return split[0], "latest"
	case 2:
		return split[0], split[1]
	default:
		panic(fmt.Errorf("unable to parse chain and version from: %s", chainVersion))
	}
}

func parseRelayerImplementation(relayerImplementationString string) ibc.RelayerImplementation {
	switch relayerImplementationString {
	case "rly":
		fallthrough
	case "cosmos/relayer":
		return ibc.CosmosRly
	case "hermes":
		return ibc.Hermes
	default:
		panic(fmt.Errorf("unknown relayer implementation provided: %s", relayerImplementationString))
	}
}

func runTestCase(testName string, testCase func(testName string, srcChain ibc.Chain, dstChain ibc.Chain, relayerImplementation ibc.RelayerImplementation) error, relayerImplementation ibc.RelayerImplementation, srcChainName, srcChainVersion, srcChainID string, srcVals int, dstChainName, dstChainVersion, dstChainID string, dstVals int) error {
	srcChain, err := ibc.GetChain(testName, srcChainName, srcChainVersion, srcChainID, srcVals, 1)
	if err != nil {
		return err
	}
	dstChain, err := ibc.GetChain(testName, dstChainName, dstChainVersion, dstChainID, dstVals, 1)
	if err != nil {
		return err
	}
	return testCase(testName, srcChain, dstChain, relayerImplementation)
}

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test IBC interactions between chains",
	Long: `Run all or specific relayer tests for source and destination chains.

e.g.
# Test cosmos relayer for two chains of gaia latest
ibc-test-framework test

# Specify specific chains/versions, relayer implementation, and test cases
ibc-test-framework test --source osmosis:v7.0.4 --destination juno:v2.3.0 --relayer rly RelayPacketTest,RelayPacketTestHeightTimeout

# Shorthand flags
ibc-test-framework test -src osmosis:v7.0.4 -dst juno:v2.3.0 -r rly RelayPacketTest
`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("IBC Test Framework")
		flags := cmd.Flags()
		srcChainNameVersion, _ := flags.GetString("src")
		dstChainNameVersion, _ := flags.GetString("dst")
		relayerImplementationString, _ := flags.GetString("relayer")
		testCasesString := args[0]

		srcChainName, srcChainVersion := parseChainVersion(srcChainNameVersion)
		dstChainName, dstChainVersion := parseChainVersion(dstChainNameVersion)

		relayerImplementation := parseRelayerImplementation(relayerImplementationString)

		srcVals, _ := flags.GetInt("src-vals")
		dstVals, _ := flags.GetInt("dst-vals")

		srcChainID, _ := flags.GetString("src-chain-id")
		dstChainID, _ := flags.GetString("dst-chain-id")

		parallel, _ := flags.GetBool("parallel")

		var testCases []func(testName string, srcChain ibc.Chain, dstChain ibc.Chain, relayerImplementation ibc.RelayerImplementation) error

		for _, testCaseString := range strings.Split(testCasesString, ",") {
			testCase, err := ibc.GetTestCase(testCaseString)
			if err != nil {
				panic(err)
			}
			testCases = append(testCases, testCase)
		}

		if parallel {
			var eg errgroup.Group
			for i, testCase := range testCases {
				testCase := testCase
				testName := fmt.Sprintf("RelayTest%d", i)
				eg.Go(func() error {
					return runTestCase(testName, testCase, relayerImplementation, srcChainName, srcChainVersion, srcChainID, srcVals, dstChainName, dstChainVersion, dstChainID, dstVals)
				})
			}
			if err := eg.Wait(); err != nil {
				panic(err)
			}
		} else {
			for i, testCase := range testCases {
				testName := fmt.Sprintf("RelayTest%d", i)
				if err := runTestCase(testName, testCase, relayerImplementation, srcChainName, srcChainVersion, srcChainID, srcVals, dstChainName, dstChainVersion, dstChainID, dstVals); err != nil {
					panic(err)
				}
			}
		}
		fmt.Println("PASS")
	},
}

func init() {
	rootCmd.AddCommand(testCmd)

	testCmd.Flags().StringP("src", "s", "gaia", "Source chain name (e.g. \"gaia\", \"gaia:v6.0.4\")")
	testCmd.Flags().StringP("dst", "d", "gaia", "Destination chain name (e.g. \"gaia\", \"gaia:v6.0.4\")")
	testCmd.Flags().StringP("relayer", "r", "rly", "Relayer implementation to use (rly or hermes)")
	testCmd.Flags().Int("src-vals", 4, "Number of Validator nodes on source chain")
	testCmd.Flags().Int("dst-vals", 4, "Number of Validator nodes on destination chain")
	testCmd.Flags().String("src-chain-id", "srcchain-1", "Chain ID to use for the source chain")
	testCmd.Flags().String("dst-chain-id", "dstchain-1", "Chain ID to use for the source chain")
	testCmd.Flags().BoolP("parallel", "p", false, "Run tests in parallel")

}
