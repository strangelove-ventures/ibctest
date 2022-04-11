package ibc

import "fmt"

var chainConfigs = []ChainConfig{
	NewCosmosChainConfig("gaia", "gaiad", "cosmos", "uatom", "0.01uatom", 1.3, "504h"),
	NewCosmosChainConfig("osmosis", "osmosisd", "osmo", "uosmo", "0.0uosmo", 1.3, "336h"),
	NewCosmosChainConfig("juno", "junod", "juno", "ujuno", "0.0ujuno", 1.3, "672h"),
}

var chainConfigMap map[string]ChainConfig

func init() {
	chainConfigMap = make(map[string]ChainConfig)
	for _, chainConfig := range chainConfigs {
		chainConfigMap[chainConfig.Name] = chainConfig
	}
}

func GetChain(testName, name, version, chainID string, numValidators, numFullNodes int) (Chain, error) {
	chainConfig, exists := chainConfigMap[name]
	if !exists {
		return nil, fmt.Errorf("No chain configuration for %s", name)
	}
	chainConfig.Version = version
	chainConfig.ChainID = chainID

	switch chainConfig.Type {
	case "cosmos":
		return NewCosmosChain(testName, chainConfig, numValidators, numFullNodes), nil
	default:
		return nil, fmt.Errorf("unexpected error, unknown chain type: %s for chain: %s", chainConfig.Type, name)
	}
}
