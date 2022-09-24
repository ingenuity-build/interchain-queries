package runner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/ingenuity-build/interchain-queries/pkg/config"
	qstypes "github.com/ingenuity-build/quicksilver/x/interchainquery/types"
	lensclient "github.com/strangelove-ventures/lens/client"
	lensquery "github.com/strangelove-ventures/lens/client/query"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	tmquery "github.com/tendermint/tendermint/libs/pubsub/query"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	clienttypes "github.com/cosmos/ibc-go/v5/modules/core/02-client/types"
	tmclient "github.com/cosmos/ibc-go/v5/modules/light-clients/07-tendermint/types"
	tmtypes "github.com/tendermint/tendermint/types"
)

type Clients []*lensclient.ChainClient

var (
	WaitInterval = time.Second * 3
	clients      = Clients{}
	ctx          = context.Background()
	sendQueue    = map[string]chan sdk.Msg{}
)

func (clients Clients) GetForChainId(chainId string) *lensclient.ChainClient {
	for _, client := range clients {
		if client.Config.ChainID == chainId {
			return client
		}
	}
	return nil
}

func Run(cfg *config.Config, home string) error {
	defer Close()
	for _, c := range cfg.Chains {
		client, err := lensclient.NewChainClient(nil, c, home, os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
		sendQueue[client.Config.ChainID] = make(chan sdk.Msg)
		clients = append(clients, client)
	}

	fmt.Println("clients", clients, cfg.DefaultChain)
	query := tmquery.MustParse(fmt.Sprintf("message.module='%s'", "interchainquery"))

	wg := &sync.WaitGroup{}

	defaultClient := clients.GetForChainId(cfg.DefaultChain)
	err := defaultClient.RPCClient.Start()
	if err != nil {
		fmt.Println(err)
	}

	ch, err := defaultClient.RPCClient.Subscribe(ctx, defaultClient.Config.ChainID+"-icq", query.String())
	if err != nil {
		fmt.Println(err)
		return err
	}
	wg.Add(1)
	go func(chainId string, ch <-chan coretypes.ResultEvent) {
		for v := range ch {
			v.Events["source"] = []string{chainId}
			go handleEvent(v)
		}
	}(defaultClient.Config.ChainID, ch)

	fmt.Println("Client started")

	wg.Add(1)
	go FlushSendQueue(defaultClient.Config.ChainID)
	fmt.Println("Flusher started")

	for _, client := range clients {
		if client.Config.ChainID != cfg.DefaultChain {
			go func(c *lensclient.ChainClient) {
			CNT:
				for {
					fmt.Println(client)
					req := &qstypes.QueryRequestsRequest{
						Pagination: &querytypes.PageRequest{Limit: 500},
						ChainId:    client.Config.ChainID,
					}

					bz := c.Codec.Marshaler.MustMarshal(req)
					fmt.Println("Fetching historic queries for chain", client.Config.ChainID)
					res, err := c.RPCClient.ABCIQuery(ctx, "/quicksilver.interchainquery.v1.QuerySrvr/Queries", bz)
					if err != nil {
						if strings.Contains(err.Error(), "Client.Timeout") {
							fmt.Println("Timeout: ", err)
							continue CNT
						}
					}
					fmt.Println("Fetched historic queries for chain", client.Config.ChainID)
					out := &qstypes.QueryRequestsResponse{}
					err = c.Codec.Marshaler.Unmarshal(res.Response.Value, out)
					if err != nil {
						fmt.Println("Unable to unmarshal: ", err)
						continue CNT
					}
					fmt.Printf("Got %d queries\n", len(out.Queries))
					go handleHistoricRequests(out.Queries, c.Config.ChainID)
					time.Sleep(30 * time.Second)

				}
			}(defaultClient)
			wg.Add(1)
		}
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
	Height        int64
	Request       []byte
}

func handleHistoricRequests(queries []qstypes.Query, sourceChainId string) {

	if len(queries) == 0 {
		return
	}

	sort.Slice(queries, func(i, j int) bool {
		return queries[i].LastEmission.GT(queries[j].LastEmission)
	})

	for _, query := range queries[0:int(math.Min(float64(len(queries)), float64(50)))] {
		q := Query{}
		q.SourceChainId = sourceChainId
		q.ChainId = query.ChainId
		q.ConnectionId = query.ConnectionId
		q.Height = 0
		q.QueryId = query.Id
		q.Request = query.Request
		q.Type = query.QueryType
		fmt.Println("Handling existing query " + q.QueryId + " for " + q.SourceChainId)
		go doRequest(q)
	}
}

func handleEvent(event coretypes.ResultEvent) {
	queries := []Query{}
	source := event.Events["source"]
	connections := event.Events["message.connection_id"]
	chains := event.Events["message.chain_id"]
	queryIds := event.Events["message.query_id"]
	types := event.Events["message.type"]
	request := event.Events["message.request"]
	height := event.Events["message.height"]

	items := len(queryIds)

	for i := 0; i < items; i++ {
		req, err := hex.DecodeString(request[i])
		if err != nil {
			panic(err)
		}
		h, err := strconv.ParseInt(height[i], 10, 64)
		if err != nil {
			panic(err)
		}
		queries = append(queries, Query{source[0], connections[i], chains[i], queryIds[i], types[i], h, req})
	}

	for _, q := range queries {
		go doRequest(q)
	}
}

func RunGRPCQuery(ctx context.Context, client *lensclient.ChainClient, method string, reqBz []byte, md metadata.MD) (abcitypes.ResponseQuery, metadata.MD, error) {

	// parse height header
	height, err := lensclient.GetHeightFromMetadata(md)
	if err != nil {
		return abcitypes.ResponseQuery{}, nil, err
	}

	prove, err := lensclient.GetProveFromMetadata(md)
	if err != nil {
		return abcitypes.ResponseQuery{}, nil, err
	}

	abciReq := abcitypes.RequestQuery{
		Path:   method,
		Data:   reqBz,
		Height: height,
		Prove:  prove,
	}

	abciRes, err := client.QueryABCI(ctx, abciReq)
	if err != nil {
		return abcitypes.ResponseQuery{}, nil, err
	}
	return abciRes, md, nil
}

func retryLightblock(ctx context.Context, client *lensclient.ChainClient, height int64, maxTime int) (*tmtypes.LightBlock, error) {
	interval := 1
	lightBlock, err := client.LightProvider.LightBlock(ctx, height)
	if err != nil {
		for {
			time.Sleep(time.Duration(interval) * time.Second)
			fmt.Println("Requerying lightblock")
			lightBlock, err = client.LightProvider.LightBlock(ctx, height)
			interval = interval + 1
			if err == nil {
				break
			} else if interval > maxTime {
				return nil, fmt.Errorf("unable to query light block, max interval exceeded")
			}
		}
	}
	return lightBlock, err
}

func doRequest(query Query) {
	var err error
	client := clients.GetForChainId(query.ChainId)
	if client == nil {
		return
	}
	if query.Height == 0 {
		block, err := client.RPCClient.Block(ctx, nil)
		if err != nil {
			panic(err)
		}
		query.Height = block.Block.LastCommit.Height - 1
	}

	fmt.Println(query.Type)
	newCtx := lensclient.SetHeightOnContext(ctx, query.Height)
	pathParts := strings.Split(query.Type, "/")
	if pathParts[len(pathParts)-1] == "key" { // fetch proof if the query is 'key'
		newCtx = lensclient.SetProveOnContext(newCtx, true)
	}
	inMd, ok := metadata.FromOutgoingContext(newCtx)
	if !ok {
		panic("failed on not ok")
	}

	var res abcitypes.ResponseQuery
	submitClient := clients.GetForChainId(query.SourceChainId)

	switch query.Type {
	case "tendermint.Tx":
		req := txtypes.GetTxRequest{}
		client.Codec.Marshaler.MustUnmarshal(query.Request, &req)
		txhash, err := hex.DecodeString(req.GetHash())
		if err != nil {
			fmt.Println("Error: Could not decode txhash: ", err)
			return
		}
		resTx, err := client.RPCClient.Tx(ctx, txhash, true)
		if err != nil {
			fmt.Println("Error: Could not get tx query: ", err)
			return
		}
		resBlocks, err := getBlocksForTxResults(client.RPCClient, []*coretypes.ResultTx{resTx})
		if err != nil {
			fmt.Println("Error: Could not get blocks for txs: ", err)
			return
		}

		out, err := mkTxResult(client.Codec.TxConfig, resTx, resBlocks[resTx.Height])
		if err != nil {
			fmt.Println("Error: Could not make txresult for txs: ", err)
			return
		}

		protoTx, ok := out.Tx.GetCachedValue().(*txtypes.Tx)
		if !ok {
			fmt.Println(codes.Internal, "expected %T, got %T", txtypes.Tx{}, out.Tx.GetCachedValue())
			return
		}

		protoProof := resTx.Proof.ToProto()

		submitQuerier := lensquery.Query{Client: submitClient, Options: lensquery.DefaultOptions()}
		connection, err := submitQuerier.Ibc_Connection(query.ConnectionId)
		if err != nil {
			fmt.Println("Error: Could not get connection from chain: ", err)
			return
		}

		clientId := connection.Connection.ClientId
		header, err := getHeader(ctx, client, submitClient, clientId, out.Height-1)
		if err != nil {
			fmt.Println("Error: Could not get header: ", err)
			return
		}

		resp := qstypes.GetTxWithProofResponse{Tx: protoTx, TxResponse: out, Proof: &protoProof, Header: header}
		res.Value = client.Codec.Marshaler.MustMarshal(&resp)

	default:
		res, _, err = RunGRPCQuery(ctx, client, "/"+query.Type, query.Request, inMd)
		if err != nil {
			panic(err)
		}
	}

	// submit tx to queue
	from, _ := submitClient.GetKeyAddress()
	if pathParts[len(pathParts)-1] == "key" {

		submitQuerier := lensquery.Query{Client: submitClient, Options: lensquery.DefaultOptions()}
		connection, err := submitQuerier.Ibc_Connection(query.ConnectionId)
		if err != nil {
			fmt.Println("Error: fetching connection: ", err)
			return
		}

		clientId := connection.Connection.ClientId

		header, err := getHeader(ctx, client, submitClient, clientId, res.Height)
		if err != nil {
			fmt.Println("Error: Could not get header: ", err)
			return
		}
		anyHeader, err := clienttypes.PackHeader(header)
		if err != nil {
			fmt.Println("Error: Could not get pack header: ", err)
			return
		}

		msg := &clienttypes.MsgUpdateClient{
			ClientId: clientId, // needs to be passed in as part of request.
			Header:   anyHeader,
			Signer:   submitClient.MustEncodeAccAddr(from),
		}

		sendQueue[query.SourceChainId] <- msg
		// } else {
		// 	newCtx = lensclient.SetHeightOnContext(newCtx, int64(clientHeight.RevisionHeight)-1)
		// 	inMd, _ = metadata.FromOutgoingContext(newCtx)
		// 	fmt.Println("Rerunning Query")
		// 	res, _, err = RunGRPCQuery(ctx, client, "/"+query.Type, query.Request, inMd)
		// 	if err != nil {
		// 		panic(err)
		// 	}

		// }
	}

	msg := &qstypes.MsgSubmitQueryResponse{ChainId: query.ChainId, QueryId: query.QueryId, Result: res.Value, Height: res.Height, ProofOps: res.ProofOps, FromAddress: submitClient.MustEncodeAccAddr(from)}
	sendQueue[query.SourceChainId] <- msg
}

func getHeader(ctx context.Context, client, submitClient *lensclient.ChainClient, clientId string, requestHeight int64) (*tmclient.Header, error) {
	submitQuerier := lensquery.Query{Client: submitClient, Options: lensquery.DefaultOptions()}
	state, err := submitQuerier.Ibc_ClientState(clientId) // pass in from request
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Error: Could not get state from chain:: %q ", err.Error()))
	}
	unpackedState, err := clienttypes.UnpackClientState(state.ClientState)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Error: Could not unpack state from chain: %q ", err.Error()))
	}

	trustedHeight := unpackedState.GetLatestHeight()
	clientHeight, ok := trustedHeight.(clienttypes.Height)
	if !ok {
		return nil, errors.New("Error: Could coerce trusted height")

	}

	fmt.Println("Fetching client update for height", "height", requestHeight+1)
	newBlock, err := retryLightblock(ctx, client, int64(requestHeight+1), 5)
	if err != nil {
		//fmt.Println("Error: Could not fetch updated LC from chain - bailing: ", err) // requeue
		panic("Error: Could not fetch updated LC from chain - bailing")
	}

	trustedBlock, err := retryLightblock(ctx, client, int64(clientHeight.RevisionHeight)+1, 5)
	if err != nil {
		//fmt.Println("Error: Could not fetch trusted LC from chain - bailing: ", err) // requeue
		panic("Error: Could not fetch trusted LC from chain - bailing")
	}

	valSet := tmtypes.NewValidatorSet(newBlock.ValidatorSet.Validators)
	trustedValSet := tmtypes.NewValidatorSet(trustedBlock.ValidatorSet.Validators)
	protoVal, err := valSet.ToProto()
	if err != nil {
		fmt.Println("Error: Could not get valset from chain: ", err)
		panic("Error: Could not get valset from chain:")
	}
	trustedProtoVal, err := trustedValSet.ToProto()
	if err != nil {
		fmt.Println("Error: Could not get trusted valset from chain: ", err)
		panic("Error: Could not get trusted valset from chain:")
	}

	header := &tmclient.Header{
		SignedHeader:      newBlock.SignedHeader.ToProto(),
		ValidatorSet:      protoVal,
		TrustedHeight:     clientHeight,
		TrustedValidators: trustedProtoVal,
	}

	return header, nil
}

func getBlocksForTxResults(node rpcclient.Client, resTxs []*coretypes.ResultTx) (map[int64]*coretypes.ResultBlock, error) {

	resBlocks := make(map[int64]*coretypes.ResultBlock)

	for _, resTx := range resTxs {
		if _, ok := resBlocks[resTx.Height]; !ok {
			resBlock, err := node.Block(context.Background(), &resTx.Height)
			if err != nil {
				return nil, err
			}

			resBlocks[resTx.Height] = resBlock
		}
	}

	return resBlocks, nil
}

func mkTxResult(txConfig client.TxConfig, resTx *coretypes.ResultTx, resBlock *coretypes.ResultBlock) (*sdk.TxResponse, error) {
	txb, err := txConfig.TxDecoder()(resTx.Tx)
	if err != nil {
		return nil, err
	}
	p, ok := txb.(intoAny)
	if !ok {
		return nil, fmt.Errorf("expecting a type implementing intoAny, got: %T", txb)
	}
	any := p.AsAny()
	return sdk.NewResponseResultTx(resTx, any, resBlock.Block.Time.Format(time.RFC3339)), nil
}

type intoAny interface {
	AsAny() *codectypes.Any
}

func FlushSendQueue(chainId string) {
	time.Sleep(WaitInterval)
	toSend := []sdk.Msg{}
	ch := sendQueue[chainId]

	for {
		if len(toSend) > 15 {
			flush(chainId, toSend)
			toSend = []sdk.Msg{}
		}
		select {
		case msg := <-ch:
			toSend = append(toSend, msg)
		case <-time.After(time.Millisecond * 1500):
			flush(chainId, toSend)
			toSend = []sdk.Msg{}
		}
	}
}

func flush(chainId string, toSend []sdk.Msg) {
	if len(toSend) > 0 {
		fmt.Printf("Send batch of %d messages\n", len(toSend))
		client := clients.GetForChainId(chainId)
		if client == nil {
			return
		}
		// dedupe on queryId
		msgs := unique(toSend)
		resp, err := client.SendMsgs(context.Background(), msgs, "icq/v0.5.0")
		if err != nil {
			if resp != nil && resp.Code == 19 && resp.Codespace == "sdk" {
				//if err.Error() == "transaction failed with code: 19" {
				fmt.Println("Tx in mempool")
			} else if resp != nil && resp.Code == 12 && resp.Codespace == "sdk" {
				//if err.Error() == "transaction failed with code: 19" {
				fmt.Println("Not enough gas")
			} else {
				panic(err)
			}
		}
		fmt.Printf("Sent batch of %d (deduplicated) messages\n", len(unique(toSend)))
		// zero messages

	}
}

func unique(msgSlice []sdk.Msg) []sdk.Msg {
	keys := make(map[string]bool)
	clientUpdateHeights := make(map[string]bool)

	list := []sdk.Msg{}
	for _, entry := range msgSlice {
		msg, ok := entry.(*clienttypes.MsgUpdateClient)
		if ok {
			header, _ := clienttypes.UnpackHeader(msg.Header)
			key := header.GetHeight().String()
			if _, value := clientUpdateHeights[key]; !value {
				clientUpdateHeights[key] = true
				list = append(list, entry)
				fmt.Println("Added ClientUpdate message")
			}
			continue
		}
		msg2, ok2 := entry.(*qstypes.MsgSubmitQueryResponse)
		if ok2 {
			if _, value := keys[msg2.QueryId]; !value {
				keys[msg2.QueryId] = true
				list = append(list, entry)
				fmt.Println("Added SubmitResponse message")
			}
		}
	}
	return list
}

func Close() error {

	query := tmquery.MustParse(fmt.Sprintf("message.module='%s'", "interchainquery"))

	for _, client := range clients {
		err := client.RPCClient.Unsubscribe(ctx, client.Config.ChainID+"-icq", query.String())
		if err != nil {
			return err
		}
	}
	return nil
}
