package presenter

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/strangelove-ventures/ibctest/internal/blockdb"
)

// CosmosMessage presents a blockdb.CosmosMessageResult.
type CosmosMessage struct {
	Result blockdb.CosmosMessageResult
}

func (msg CosmosMessage) Height() string { return strconv.FormatInt(msg.Result.Height, 10) }

// Index is the message's ordered position within the tx.
func (msg CosmosMessage) Index() string { return strconv.Itoa(msg.Result.Index) }

// Type is a URI for the proto definition, e.g. /ibc.core.client.v1.MsgCreateClient
func (msg CosmosMessage) Type() string { return msg.Result.Type }

// IBCDetails varies based on the presence of IBC data within the message.
func (msg CosmosMessage) IBCDetails() string {
	res := msg.Result
	var parts []string
	for _, field := range []struct {
		Label string
		Val   sql.NullString
	}{
		{"ClientChain", res.ClientChainID},
		{"Client", res.ClientID},
		{"Counterparty Client", res.CounterpartyClientID},
		{"Connection", res.ConnID},
		{"Counterparty Connection", res.CounterpartyConnID},
		{"Channel", res.ChannelID},
		{"Port", res.PortID},
		{"Counterparty Channel", res.CounterpartyChannelID},
		{"Counterparty Port", res.CounterpartyPortID},
	} {
		if !field.Val.Valid {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", field.Label, field.Val.String))
	}
	return strings.Join(parts, " · ")
}
