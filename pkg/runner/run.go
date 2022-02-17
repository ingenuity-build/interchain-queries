package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ingenuity-build/interchain-queries/pkg/config"
	qstypes "github.com/ingenuity-build/quicksilver/x/interchainquery/types"
	"github.com/strangelove-ventures/lens/client"
	tmquery "github.com/tendermint/tendermint/libs/pubsub/query"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
)

type Clients []*client.ChainClient

var (
	clients = Clients{}
	ctx     = context.Background()
)

func (clients Clients) GetForChainId(chainId string) *client.ChainClient {
	for _, client := range clients {
		if client.ChainId() == chainId {
			return client
		}
	}
	return nil
}

func Run(cfg *config.Config, home string) error {
	defer Close()
	for _, c := range cfg.Chains {
		client, err := client.NewChainClient(c, home, os.Stdin, os.Stdout)
		fmt.Println(client)
		if err != nil {
			return err
		}
		clients = append(clients, client)
	}

	query := tmquery.MustParse(fmt.Sprintf("message.module='%s'", "interchainquery"))

	wg := &sync.WaitGroup{}
	for _, client := range clients {
		err := client.RPCClient.Start()
		if err != nil {
			fmt.Println(err)
		}

		ch, err := client.RPCClient.Subscribe(ctx, client.ChainId()+"-icq", query.String())
		if err != nil {
			fmt.Println(err)
			return err
		}
		wg.Add(1)
		go func(chainId string, ch <-chan coretypes.ResultEvent) {
			for v := range ch {
				v.Events["source"] = []string{chainId}
				fmt.Printf("v.Events: %v\n", v.Events)
				go handleEvent(v)
			}
		}(client.ChainId(), ch)
	}

	wg.Wait()
	return nil
}

type Query struct {
	SourceChainId string
	ConnectionId  string
	ChainId       string
	QueryId       string
	Type          string
	Params        map[string]string
}

func handleEvent(event coretypes.ResultEvent) {
	queries := []Query{}
	source := event.Events["source"]
	connections := event.Events["message.connection_id"]
	chains := event.Events["message.chain_id"]
	queryIds := event.Events["message.query_id"]
	types := event.Events["message.type"]
	params := event.Events["message.parameters"]

	items := len(queryIds)

	for i := 0; i < items; i++ {
		query_params := parseParams(params, queryIds[i])
		queries = append(queries, Query{source[0], connections[i], chains[i], queryIds[i], types[i], query_params})
	}
	// fmt.Println("----------------------")
	// fmt.Println(queries)
	// fmt.Println("----------------------")
	for _, q := range queries {
		go doRequest(q)
	}
}

func parseParams(params []string, query_id string) map[string]string {
	out := map[string]string{}
	for _, p := range params {
		if strings.HasPrefix(p, query_id) {
			parts := strings.SplitN(p, ":", 3)
			out[parts[1]] = parts[2]
		}
	}
	return out
}

func doRequest(query Query) {
	client := clients.GetForChainId(query.ChainId)
	if client == nil {
		fmt.Println("No chain")
		return
	}
	fmt.Println(query.Type)
	var data []byte
	var err error

	switch query.Type {
	case "cosmos.bank.v1beta1.Query/AllBalances":
		balances, _ := client.QueryBalanceWithAddress(query.Params["address"])
		data, err = json.Marshal(&balances)
		if err != nil {
			panic(err)
		}

	}
	sendTx(clients.GetForChainId(query.SourceChainId), query, data)

}

func sendTx(client *client.ChainClient, query Query, data []byte) error {
	from, _ := client.Address()
	msg := &qstypes.MsgSubmitQueryResponse{query.ChainId, query.QueryId, data, 0, from}
	resp, err := client.SendMsg(context.Background(), msg)

	fmt.Println(resp)
	if err != nil {
		if resp.Code == 19 && resp.Codespace == "sdk" {
			//if err.Error() == "transaction failed with code: 19" {
			fmt.Println("Tx in mempool")
		} else {
			panic(err)
		}
	}
	return nil
}

func Close() error {

	query := tmquery.MustParse(fmt.Sprintf("message.module='%s'", "interchainquery"))

	for _, client := range clients {
		err := client.RPCClient.Unsubscribe(ctx, client.ChainId()+"-icq", query.String())
		if err != nil {
			return err
		}
	}
	return nil
}
