package keeper

import (
	"bytes"
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/types"
	bech32ibctypes "github.com/althea-net/bech32-ibc/x/bech32ibc/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

func TestGetAndDeleteAttestation(t *testing.T) {
	input := CreateTestEnv(t)
	k := input.GravityKeeper
	ctx := input.Context

	length := 10
	for _, evmChain := range k.GetEvmChains(ctx) {
		_, _, hashes := createAttestations(t, ctx, k, length, evmChain.EvmChainPrefix)

		// Get created attestations
		for i := 0; i < length; i++ {
			nonce := uint64(1 + i)
			att := k.GetAttestation(ctx, evmChain.EvmChainPrefix, nonce, hashes[i])
			require.NotNil(t, att)
		}
		recentAttestations := k.GetMostRecentAttestations(ctx, evmChain.EvmChainPrefix, uint64(length))
		require.True(t, len(recentAttestations) == length)

		// Delete last 3 attestations
		var nilAtt *types.Attestation
		for i := 7; i < length; i++ {
			nonce := uint64(1 + i)
			att := k.GetAttestation(ctx, evmChain.EvmChainPrefix, nonce, hashes[i])
			k.DeleteAttestation(ctx, *att)

			att = k.GetAttestation(ctx, evmChain.EvmChainPrefix, nonce, hashes[i])
			require.Equal(t, nilAtt, att)
		}
		recentAttestations = k.GetMostRecentAttestations(ctx, evmChain.EvmChainPrefix, uint64(10))
		require.True(t, len(recentAttestations) == 7)

		// Check all attestations again
		for i := 0; i < 7; i++ {
			nonce := uint64(1 + i)
			att := k.GetAttestation(ctx, evmChain.EvmChainPrefix, nonce, hashes[i])
			require.NotNil(t, att)
		}
		for i := 7; i < length; i++ {
			nonce := uint64(1 + i)
			att := k.GetAttestation(ctx, evmChain.EvmChainPrefix, nonce, hashes[i])
			require.Equal(t, nilAtt, att)
		}
	}
}

// Sets up 10 attestations and checks that they are returned in the correct order
func TestGetMostRecentAttestations(t *testing.T) {
	input := CreateTestEnv(t)
	defer func() { input.Context.Logger().Info("Asserting invariants at test end"); input.AssertInvariants() }()

	k := input.GravityKeeper
	ctx := input.Context

	length := 10
	for _, evmChain := range k.GetEvmChains(ctx) {
		msgs, anys, _ := createAttestations(t, ctx, k, length, evmChain.EvmChainPrefix)

		recentAttestations := k.GetMostRecentAttestations(ctx, evmChain.EvmChainPrefix, uint64(length))
		require.True(t, len(recentAttestations) == length,
			"recentAttestations should have len %v but instead has %v", length, len(recentAttestations))
		for n, attest := range recentAttestations {
			require.Equal(t, attest.Claim.GetCachedValue(), anys[n].GetCachedValue(),
				"The %vth claim does not match our message: claim %v\n message %v", n, attest.Claim, msgs[n])
		}
	}
}

func createAttestations(t *testing.T, ctx sdk.Context, k Keeper, length int, evmChainPrefix string) ([]types.MsgSendToCosmosClaim, []codectypes.Any, [][]byte) {
	msgs := make([]types.MsgSendToCosmosClaim, 0, length)
	anys := make([]codectypes.Any, 0, length)
	hashes := make([][]byte, 0, length)
	for i := 0; i < length; i++ {
		nonce := uint64(1 + i)

		contract := common.BytesToAddress(bytes.Repeat([]byte{0x1}, 20)).String()
		sender := common.BytesToAddress(bytes.Repeat([]byte{0x2}, 20)).String()
		orch := sdk.AccAddress(bytes.Repeat([]byte{0x3}, 20)).String()
		receiver := sdk.AccAddress(bytes.Repeat([]byte{0x4}, 20)).String()
		msg := types.MsgSendToCosmosClaim{
			EventNonce:     nonce,
			EthBlockHeight: 1,
			TokenContract:  contract,
			Amount:         sdk.NewInt(10000000000 + int64(i)),
			EthereumSender: sender,
			CosmosReceiver: receiver,
			Orchestrator:   orch,
			EvmChainPrefix: evmChainPrefix,
		}
		msgs = append(msgs, msg)

		any, err := codectypes.NewAnyWithValue(&msg)
		require.NoError(t, err)
		anys = append(anys, *any)
		att := &types.Attestation{
			Observed: false,
			Votes:    []string{},
			Height:   uint64(ctx.BlockHeight()),
			Claim:    any,
		}
		unpackedClaim, err := k.UnpackAttestationClaim(att)
		if err != nil {
			panic(fmt.Sprintf("Bad new attestation: %s", err.Error()))
		}
		err = unpackedClaim.ValidateBasic()
		if err != nil {
			panic(fmt.Sprintf("Bad claim discovered: %s", err.Error()))
		}
		hash, err := msg.ClaimHash()
		hashes = append(hashes, hash)
		require.NoError(t, err)
		k.SetAttestation(ctx, evmChainPrefix, nonce, hash, att)
	}

	return msgs, anys, hashes
}

func TestGetSetLastObservedEvmChainBlockHeight(t *testing.T) {
	input := CreateTestEnv(t)
	k := input.GravityKeeper
	ctx := input.Context

	ethereumHeight := uint64(7654321)

	for _, evmChain := range k.GetEvmChains(ctx) {
		require.NotPanics(t, func() { k.SetLastObservedEvmChainBlockHeight(ctx, evmChain.EvmChainPrefix, ethereumHeight) })

		ethHeight := k.GetLastObservedEvmChainBlockHeight(ctx, evmChain.EvmChainPrefix)
		require.Equal(t, uint64(ctx.BlockHeight()), ethHeight.CosmosBlockHeight)
		require.Equal(t, ethereumHeight, ethHeight.EthereumBlockHeight)
	}
}

func TestGetSetLastObservedValset(t *testing.T) {
	input := CreateTestEnv(t)
	k := input.GravityKeeper
	ctx := input.Context

	setValset := types.Valset{
		Nonce:  1,
		Height: 1,
		Members: []types.BridgeValidator{
			{
				Power:           999999999,
				EthereumAddress: "0x0000000000000001",
			},
			{
				Power:           999999999,
				EthereumAddress: "0x0000000000000002",
			},
			{
				Power:           999999999,
				EthereumAddress: "0x0000000000000003",
			},
		},
		RewardAmount: sdk.NewInt(1000000000),
		RewardToken:  "footoken",
	}

	for _, evmChain := range k.GetEvmChains(ctx) {
		require.NotPanics(t, func() { k.SetLastObservedValset(ctx, evmChain.EvmChainPrefix, setValset) })

		getValset := k.GetLastObservedValset(ctx, evmChain.EvmChainPrefix)
		require.EqualValues(t, setValset, *getValset)
	}
}

func TestGetSetLastEventNonceByValidator(t *testing.T) {
	input := CreateTestEnv(t)
	k := input.GravityKeeper
	ctx := input.Context

	valAddrString := "gravity1ahx7f8wyertuus9r20284ej0asrs085ceqtfnm"
	valAccAddress, err := sdk.AccAddressFromBech32(valAddrString)
	require.NoError(t, err)
	valAccount := k.accountKeeper.NewAccountWithAddress(ctx, valAccAddress)
	require.NotNil(t, valAccount)

	nonce := uint64(1234)
	addrInBytes := valAccount.GetAddress().Bytes()

	for _, evmChain := range k.GetEvmChains(ctx) {
		// In case this is first time validator is submiting claim, nonce is expected to be LastObservedNonce-1
		k.setLastObservedEventNonce(ctx, evmChain.EvmChainPrefix, nonce)
		getEventNonce := k.GetLastEventNonceByValidator(ctx, evmChain.EvmChainPrefix, addrInBytes)
		require.Equal(t, nonce-1, getEventNonce)

		require.NotPanics(t, func() { k.SetLastEventNonceByValidator(ctx, evmChain.EvmChainPrefix, addrInBytes, nonce) })

		getEventNonce = k.GetLastEventNonceByValidator(ctx, evmChain.EvmChainPrefix, addrInBytes)
		require.Equal(t, nonce, getEventNonce)
	}
}

func TestInvalidHeight(t *testing.T) {
	input, ctx := SetupFiveValChain(t)
	defer func() { input.Context.Logger().Info("Asserting invariants at test end"); input.AssertInvariants() }()
	pk := input.GravityKeeper
	msgServer := NewMsgServerImpl(pk)
	log := ctx.Logger()
	evmChain := pk.GetEvmChainData(ctx, EthChainPrefix)

	val0 := ValAddrs[0]
	orch0 := OrchAddrs[0]
	sender := AccAddrs[0]
	receiver := EthAddrs[0]
	lastNonce := pk.GetLastObservedEventNonce(ctx, evmChain.EvmChainPrefix)
	lastEthHeight := pk.GetLastObservedEvmChainBlockHeight(ctx, evmChain.EvmChainPrefix).EthereumBlockHeight
	lastBatchNonce := 0
	tokenContract := "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599"
	goodHeight := lastEthHeight + 1
	batchTimeout := lastEthHeight + 100
	badHeight := batchTimeout

	// Setup a batch with a timeout
	batch := types.OutgoingTxBatch{
		BatchNonce:   uint64(lastBatchNonce + 1),
		BatchTimeout: batchTimeout,
		Transactions: []types.OutgoingTransferTx{{
			Id:          0,
			Sender:      sender.String(),
			DestAddress: receiver.String(),
			Erc20Token: types.ERC20Token{
				Contract: tokenContract,
				Amount:   sdk.NewInt(1),
			},
			Erc20Fee: types.ERC20Token{
				Contract: tokenContract,
				Amount:   sdk.NewInt(1),
			},
		}},
		TokenContract:      tokenContract,
		CosmosBlockCreated: 0,
	}
	b, err := batch.ToInternal()
	require.NoError(t, err)
	pk.StoreBatch(ctx, evmChain.EvmChainPrefix, *b)

	// Submit a bad claim with BlockHeight >= timeout

	bad := types.MsgBatchSendToEthClaim{
		EventNonce:     lastNonce + 1,
		EthBlockHeight: badHeight,
		BatchNonce:     uint64(lastBatchNonce + 1),
		TokenContract:  tokenContract,
		Orchestrator:   orch0.String(),
		EvmChainPrefix: evmChain.EvmChainPrefix,
	}
	err = bad.ValidateBasic()
	require.NoError(t, err)

	context := sdk.WrapSDKContext(ctx)
	log.Info("Submitting bad eth claim from orchestrator 0", "orch", orch0.String(), "val", val0.String())

	// BatchSendToEthClaim is supposed to panic and fail the message execution, set up a defer recover to catch it
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered from panic:", r)
		} else {
			panic("Expected to find a panic coming from BatchSendToEthClaim()!")
		}
	}()
	_, err = msgServer.BatchSendToEthClaim(context, &bad)
	require.NoError(t, err)

	// Assert that there is no attestation since the above panicked
	badHash, err := bad.ClaimHash()
	require.NoError(t, err)
	att := pk.GetAttestation(ctx, EthChainPrefix, bad.GetEventNonce(), badHash)
	require.Nil(t, att)

	// Attest the actual batch, and assert the votes are correct
	for i, orch := range OrchAddrs[1:] {
		log.Info("Submitting good eth claim from orchestrators", "orch", orch.String())
		good := types.MsgBatchSendToEthClaim{
			EventNonce:     lastNonce + 1,
			EthBlockHeight: goodHeight,
			BatchNonce:     uint64(lastBatchNonce + 1),
			TokenContract:  tokenContract,
			Orchestrator:   orch.String(),
			EvmChainPrefix: evmChain.EvmChainPrefix,
		}
		_, err := msgServer.BatchSendToEthClaim(context, &good)
		require.NoError(t, err)

		goodHash, err := good.ClaimHash()
		require.NoError(t, err)
		require.Equal(t, badHash, goodHash) // The hash should be the same, even though that's wrong

		att := pk.GetAttestation(ctx, EthChainPrefix, good.GetEventNonce(), goodHash)
		require.NotNil(t, att)
		log.Info("Asserting that the bad attestation only has one claimer", "attVotes", att.Votes)
		require.Equal(t, len(att.Votes), i+1) // Only these good orchestrators votes should be counted
	}

}

func TestSendCoinToCosmosAccount(t *testing.T) {
	// setup
	input := CreateTestEnv(t)
	k := input.GravityKeeper
	ctx := input.Context

	nonce := uint64(1)
	claim := types.MsgSendToCosmosClaim{
		EventNonce:     nonce,
		EthBlockHeight: 1,
		TokenContract:  "0xdafea492d9c6733ae3d56b7ed1adb60692c98bc5",
		Amount:         sdk.NewInt(10000000000 + int64(1)),
		EthereumSender: "0x00000000000000000002",
		CosmosReceiver: "0x993d06FC97F45f16e4805883b98a6c20BAb54964",
		Orchestrator:   "0x00000000000000000004",
		EvmChainPrefix: EthChainPrefix,
	}

	attestationHandler := AttestationHandler{
		keeper: &k,
	}

	// Check if coin is Cosmos-originated asset and get denom
	tokenAddress, err := types.NewEthAddress(claim.TokenContract)
	require.NoError(t, err)
	isCosmosOriginated, denom := k.ERC20ToDenomLookup(ctx, claim.EvmChainPrefix, *tokenAddress)
	require.Equal(t, isCosmosOriginated, false)

	coin := sdk.NewCoin(denom, claim.Amount)

	input.GravityKeeper.bech32IbcKeeper.SetHrpIbcRecords(ctx, []bech32ibctypes.HrpIbcRecord{
		{
			Hrp:           "oraib",
			SourceChannel: "channel-99",
		},
	})

	// action

	// cosmos receiver is not bech32 case
	// Validate the receiver as a valid bech32 address
	sourceChannel, _, _, accountPrefix, receiverAddress, err := types.ParseReceiver(claim.CosmosReceiver)
	isIbcQueued, sendError := attestationHandler.sendCoinToCosmosAccount(ctx, claim, receiverAddress, sourceChannel, accountPrefix, coin)
	require.Error(t, sendError)
	require.Equal(t, isIbcQueued, false)

	// equal account prefix case - gravity
	claim.CosmosReceiver = "gravity1603j3e4juddh7cuhfquxspl0p0nsun047vzxk8"
	sourceChannel, _, _, accountPrefix, receiverAddress, err = types.ParseReceiver(claim.CosmosReceiver)
	require.NoError(t, err)
	// mint new ethereum based coins to send to receiver
	k.bankKeeper.MintCoins(ctx, types.ModuleName, sdk.NewCoins(sdk.NewCoin("ethereum0xDAFEA492D9c6733ae3d56b7Ed1ADB60692c98Bc5", claim.Amount)))
	isIbcQueued, sendError = attestationHandler.sendCoinToCosmosAccount(ctx, claim, receiverAddress, sourceChannel, accountPrefix, coin)
	require.NoError(t, sendError)
	require.Equal(t, isIbcQueued, false)

	// not equal account prefix case, meaning it will be forwarded

	// need more setup
	k.setLastObservedEventNonce(ctx, claim.EvmChainPrefix, 1)
	// mint new ethereum based coins to send to receiver
	k.bankKeeper.MintCoins(ctx, types.ModuleName, sdk.NewCoins(sdk.NewCoin("ethereum0xDAFEA492D9c6733ae3d56b7Ed1ADB60692c98Bc5", claim.Amount)))

	// first case: no channel prefix
	claim.CosmosReceiver = "oraib14n3tx8s5ftzhlxvq0w5962v60vd82h305kec0j"
	sourceChannel, _, _, accountPrefix, receiverAddress, err = types.ParseReceiver(claim.CosmosReceiver)
	require.NoError(t, err)
	// mint new ethereum based coins to send to receiver
	isIbcQueued, sendError = attestationHandler.sendCoinToCosmosAccount(ctx, claim, receiverAddress, sourceChannel, accountPrefix, coin)
	require.NoError(t, sendError)
	require.Equal(t, true, isIbcQueued)

	// get auto forward queue
	ibcForwardQueue := k.GetNextPendingIbcAutoForward(ctx, claim.EvmChainPrefix)
	hrpIbcRecord, _ := k.bech32IbcKeeper.GetHrpIbcRecord(ctx, accountPrefix)
	require.Equal(t, ibcForwardQueue.IbcChannel, hrpIbcRecord.SourceChannel)

	// second case: has channel prefix
	k.setLastObservedEventNonce(ctx, claim.EvmChainPrefix, 2)
	claim.EventNonce = 2
	claim.CosmosReceiver = "channel-0/oraib14n3tx8s5ftzhlxvq0w5962v60vd82h305kec0j"
	sourceChannel, _, _, accountPrefix, receiverAddress, err = types.ParseReceiver(claim.CosmosReceiver)
	require.NoError(t, err)
	// mint new ethereum based coins to send to receiver
	isIbcQueued, sendError = attestationHandler.sendCoinToCosmosAccount(ctx, claim, receiverAddress, sourceChannel, accountPrefix, coin)
	require.NoError(t, sendError)
	require.Equal(t, true, isIbcQueued)
	// get auto forward queue
	queues := k.PendingIbcAutoForwards(ctx, claim.EvmChainPrefix, 2)
	require.Equal(t, sourceChannel, queues[1].IbcChannel)
}

func TestGetPrefixFromBech32(t *testing.T) {
	cosmosReceiver := "oraib14n3tx8s5ftzhlxvq0w5962v60vd82h305kec0j"
	accountPrefix, prefixErr := types.GetPrefixFromBech32(cosmosReceiver)
	require.NoError(t, prefixErr)
	require.Equal(t, "oraib", accountPrefix)

	// if cosmos receiver has channel in front, then the prefix should be channel-x/oraib
	cosmosReceiver = "channel-0/oraib14n3tx8s5ftzhlxvq0w5962v60vd82h305kec0j"
	accountPrefix, prefixErr = types.GetPrefixFromBech32(cosmosReceiver)
	require.NoError(t, prefixErr)
	require.Equal(t, "channel-0/oraib", accountPrefix)
}

func TestExpectedSupplyChange(t *testing.T) {
	// Create one of each type of claim, make attestations for those claims, check that the value from
	// pk.ExpectedSupplyChange() returns the value contained in the attestation and nothing else
	input, ctx := SetupFiveValChain(t)
	defer func() { input.Context.Logger().Info("Asserting invariants at test end"); input.AssertInvariants() }()
	pk := input.GravityKeeper
	transfers := []*types.InternalERC20Token{
		{
			Contract: TokenContracts[0],
			Amount:   sdk.NewInt(1_000000),
		},
		{
			Contract: TokenContracts[0],
			Amount:   sdk.NewInt(2_000000),
		},
	}
	fees := []*types.InternalERC20Token{
		{
			Contract: TokenContracts[0],
			Amount:   sdk.NewInt(1_000000),
		},
		{
			Contract: TokenContracts[0],
			Amount:   sdk.NewInt(2_000000),
		},
	}
	transfersTotal := types.InternalERC20Tokens(transfers).ToCoins(EthChainPrefix)
	feesTotal := types.InternalERC20Tokens(fees).ToCoins(EthChainPrefix)
	batchTotal := transfersTotal.Add(feesTotal...)
	// setup claims which get fetched
	batch := types.InternalOutgoingTxBatch{
		BatchNonce:   1,
		BatchTimeout: 0,
		Transactions: []*types.InternalOutgoingTransferTx{
			{
				Id:          1,
				Sender:      OrchAddrs[0],
				DestAddress: types.EthAddressFromCommon(EthAddrs[0]),
				Erc20Token:  transfers[0],
				Erc20Fee:    fees[0],
			},
			{
				Id:          2,
				Sender:      OrchAddrs[0],
				DestAddress: types.EthAddressFromCommon(EthAddrs[0]),
				Erc20Token:  transfers[1],
				Erc20Fee:    fees[1],
			},
		},
		TokenContract:      TokenContracts[0],
		CosmosBlockCreated: 0,
	}
	pk.StoreBatch(ctx, EthChainPrefix, batch)

	logicCall := types.InternalOutgoingLogicCall{
		Transfers:            transfers,
		Fees:                 fees,
		LogicContractAddress: *types.EthAddressFromCommon(EthAddrs[1]),
		Payload:              []byte{0x0},
		Timeout:              0,
		InvalidationId:       []byte{0x0},
		InvalidationNonce:    0,
		CosmosBlockCreated:   0,
	}
	pk.SetOutgoingLogicCall(ctx, EthChainPrefix, logicCall.ToExternal())

	tokenDenoms := make([]string, len(TokenContracts))
	for i, c := range TokenContracts {
		tokenDenoms[i] = types.GravityDenom(EthChainPrefix, c)
	}
	sendToCosmos := types.MsgSendToCosmosClaim{
		EventNonce:     1,
		EthBlockHeight: 123,
		EvmChainPrefix: EthChainPrefix,
		TokenContract:  TokenContractAddrs[0],
		Amount:         sdk.NewInt(100),
		EthereumSender: EthAddrs[1].String(),
		CosmosReceiver: OrchAddrs[1].String(),
		Orchestrator:   OrchAddrs[1].String(),
	}
	stcAny, err := codectypes.NewAnyWithValue(&sendToCosmos)
	require.NoError(t, err)
	sendToEth := types.MsgBatchSendToEthClaim{
		EventNonce:     2,
		EthBlockHeight: 456,
		EvmChainPrefix: EthChainPrefix,
		BatchNonce:     batch.BatchNonce,
		TokenContract:  batch.TokenContract.GetAddress().String(),
		Orchestrator:   OrchAddrs[2].String(),
	}
	steAny, err := codectypes.NewAnyWithValue(&sendToEth)
	require.NoError(t, err)
	erc20Deployed := types.MsgERC20DeployedClaim{
		EventNonce:     3,
		EthBlockHeight: 789,
		CosmosDenom:    "foo",
		EvmChainPrefix: EthChainPrefix,
		TokenContract:  TokenContractAddrs[1],
		Name:           "Foo",
		Symbol:         "FOO",
		Decimals:       6,
		Orchestrator:   OrchAddrs[3].String(),
	}
	e2dAny, err := codectypes.NewAnyWithValue(&erc20Deployed)
	require.NoError(t, err)
	valsetUpdated := types.MsgValsetUpdatedClaim{
		EventNonce:     4,
		ValsetNonce:    1,
		EthBlockHeight: 1011,
		EvmChainPrefix: EthChainPrefix,
		Members:        []types.BridgeValidator{},
		RewardAmount:   sdk.NewIntFromUint64(123),
		RewardToken:    TokenContractAddrs[2],
		Orchestrator:   OrchAddrs[0].String(),
	}
	vuAny, err := codectypes.NewAnyWithValue(&valsetUpdated)
	require.NoError(t, err)
	logicCallExecuted := types.MsgLogicCallExecutedClaim{
		EventNonce:        5,
		EthBlockHeight:    1213,
		EvmChainPrefix:    EthChainPrefix,
		InvalidationId:    []byte{0x0},
		InvalidationNonce: 0,
		Orchestrator:      OrchAddrs[1].String(),
	}
	lceAny, err := codectypes.NewAnyWithValue(&logicCallExecuted)
	require.NoError(t, err)
	testcases := []struct {
		name      string
		claim     codectypes.Any
		expSupply sdk.Coins
	}{
		{
			"Send to Cosmos",
			*stcAny,
			sdk.NewCoins(sdk.NewInt64Coin(tokenDenoms[0], 100)),
		},
		{
			"Batch Send to Eth",
			*steAny,
			batchTotal,
		},
		{
			"ERC20 Deployed",
			*e2dAny,
			sdk.Coins{},
		},
		{
			"Valset Updated",
			*vuAny,
			sdk.Coins{},
		},
		{
			"Logic Call Executed",
			*lceAny,
			batchTotal,
		},
	}
	for _, testcase := range testcases {
		var claim types.EthereumClaim
		input.GravityKeeper.cdc.UnpackAny(&testcase.claim, &claim)
		output, err := pk.ExpectedSupplyChange(ctx, claim)
		require.NoError(t, err)
		if output == nil {
			require.Empty(t, testcase.expSupply, "Test case %v, got nil response but expected %v", testcase.name, testcase.expSupply)
		} else {
			require.ElementsMatchf(t, *output, testcase.expSupply, "Test case %v, expected %v but got %v", testcase.name, testcase.expSupply, output)
		}
	}
}
