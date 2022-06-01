package hermes

type HermesGlobalConfig struct {
	LogLevel string `toml:"log_level"`
}

type HermesModeConfig struct {
	Clients     HermesClientsConfig
	Connections HermesConnectionsConfig
	Channels    HermesChannelsConfig
	Packets     HermesPacketsConfig
}

type HermesClientsConfig struct {
	Enabled     bool
	Refresh     bool
	Misbehavior bool
}

type HermesConnectionsConfig struct {
	Enabled bool
}

type HermesChannelsConfig struct {
	Enabled bool
}

type HermesPacketsConfig struct {
	Enabled        bool
	ClearInterval  uint64 `toml:"clear_interval"`
	ClearOnStart   bool   `toml:"clear_on_start"`
	TxConfirmation bool   `toml:"tx_confirmation"`
}

type HermesRESTConfig struct {
	Enabled bool
	Host    string
	Port    uint16
}

type HermesTelemetryConfig struct {
	Enabled bool
	Host    string
	Port    uint16
}

type HermesGasPriceConfig struct {
	Price float64
	Denom string
}

type HermesPacketFilterConfig struct {
	Policy string
	List   [][]string
}

type HermesTrustThresholdConfig struct {
	Numerator   string
	Denominator string
}

type HermesAddressTypeConfig struct {
	Derivation string
}

type HermesChainConfig struct {
	ID             string
	RpcAddr        string                     `toml:"rpc_addr"`
	WebSocketAddr  string                     `toml:"websocket_addr"`
	GRPCAddr       string                     `toml:"grpc_addr"`
	RPCTimeout     string                     `toml:"rpc_timeout"`
	AccountPrefix  string                     `toml:"account_prefix"`
	KeyName        string                     `toml:"key_name"`
	KeyStoreType   string                     `toml:"key_store_type"`
	StorePrefix    string                     `toml:"store_prefix"`
	DefaultGas     uint64                     `toml:"default_gas,omitempty"`
	MaxGas         uint64                     `toml:"max_gas,omitempty"`
	GasAdjustment  float64                    `toml:"gas_adjustment,omitempty"`
	FeeGranter     string                     `toml:"fee_granter,omitempty"`
	MaxMsgNum      int                        `toml:"max_msg_num"`
	MaxTxSize      int                        `toml:"max_tx_size"`
	ClockDrift     string                     `toml:"clock_drift"`
	MaxBlockTime   string                     `toml:"proof_specs"`
	TrustingPeriod string                     `toml:"trusting_period,omitempty"`
	MemoPrefix     string                     `toml:"memo_prefix"`
	ProofSpecs     string                     `toml:"proof_specs"`
	TrustThreshold HermesTrustThresholdConfig `toml:"trust_threshold"`
	GasPrice       HermesGasPriceConfig       `toml:"gas_price"`
	PacketFilter   HermesPacketFilterConfig   `toml:"packet_filter"`
	AddressType    HermesAddressTypeConfig    `toml:"address_type"`
}

type HermesConfig struct {
	Global    HermesGlobalConfig
	Mode      HermesModeConfig
	REST      HermesRESTConfig
	Telemetry HermesTelemetryConfig
	Chains    []HermesChainConfig
}
