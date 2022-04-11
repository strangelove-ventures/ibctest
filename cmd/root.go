package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ibc-test-framework",
	Short: "Test complex IBC interactions between arbitrary chains and relayers",
	Long: `Testing complex IBC interactions between arbitrary chains.
 Testing multiple relayer implementations:
- cosmos/relayer
- hermes
- tsrelayer
`,
}

func Execute() {
	fmt.Println(`***************************************
********** IBC Test Framework *********
** Developed by Strangelove Ventures **
***************************************`)
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {}
