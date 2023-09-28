package keeper

import (
	"context"
	"fmt"
	"strings"

	v1 "github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/migrations/v1"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/types"
)

// nolint: exhaustruct
var _ types.QueryServer = Keeper{}

const MERCURY_UPGRADE_HEIGHT uint64 = 1282013
const QUERY_ATTESTATIONS_LIMIT uint64 = 1000


// Params queries the params of the gravity module
func (k Keeper) Params(c context.Context, req *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	var params types.Params
	k.paramSpace.GetParamSet(sdk.UnwrapSDKContext(c), &params)
	return &types.QueryParamsResponse{Params: params}, nil
}

// CurrentValset queries the CurrentValset of the gravity module
func (k Keeper) CurrentValset(
	c context.Context,
	req *types.QueryCurrentValsetRequest) (*types.QueryCurrentValsetResponse, error) {
	vs, err := k.GetCurrentValset(sdk.UnwrapSDKContext(c), req.EvmChainPrefix)
	if err != nil {
		// nolint: exhaustruct
		return &types.QueryCurrentValsetResponse{}, err
	}
	return &types.QueryCurrentValsetResponse{Valset: vs}, nil
}

// ValsetRequest queries the ValsetRequest of the gravity module
func (k Keeper) ValsetRequest(
	c context.Context,
	req *types.QueryValsetRequestRequest) (*types.QueryValsetRequestResponse, error) {
	return &types.QueryValsetRequestResponse{Valset: k.GetValset(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, req.Nonce)}, nil
}

// ValsetConfirm queries the ValsetConfirm of the gravity module
func (k Keeper) ValsetConfirm(
	c context.Context,
	req *types.QueryValsetConfirmRequest) (*types.QueryValsetConfirmResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "address invalid")
	}
	return &types.QueryValsetConfirmResponse{Confirm: k.GetValsetConfirm(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, req.Nonce, addr)}, nil
}

// ValsetConfirmsByNonce queries the ValsetConfirmsByNonce of the gravity module
func (k Keeper) ValsetConfirmsByNonce(
	c context.Context,
	req *types.QueryValsetConfirmsByNonceRequest) (*types.QueryValsetConfirmsByNonceResponse, error) {
	confirms := k.GetValsetConfirms(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, req.Nonce)

	return &types.QueryValsetConfirmsByNonceResponse{Confirms: confirms}, nil
}

const maxValsetRequestsReturned = 5

// LastValsetRequests queries the LastValsetRequests of the gravity module
func (k Keeper) LastValsetRequests(
	c context.Context,
	req *types.QueryLastValsetRequestsRequest) (*types.QueryLastValsetRequestsResponse, error) {
	valReq := k.GetValsets(sdk.UnwrapSDKContext(c), req.EvmChainPrefix)
	valReqLen := len(valReq)
	retLen := 0
	if valReqLen < maxValsetRequestsReturned {
		retLen = valReqLen
	} else {
		retLen = maxValsetRequestsReturned
	}
	return &types.QueryLastValsetRequestsResponse{Valsets: valReq[0:retLen]}, nil
}

// LastPendingValsetRequestByAddr queries the LastPendingValsetRequestByAddr of the gravity module
func (k Keeper) LastPendingValsetRequestByAddr(
	c context.Context,
	req *types.QueryLastPendingValsetRequestByAddrRequest) (*types.QueryLastPendingValsetRequestByAddrResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "address invalid")
	}

	var pendingValsetReq []types.Valset
	k.IterateValsets(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, func(_ []byte, val *types.Valset) bool {
		// foundConfirm is true if the operatorAddr has signed the valset we are currently looking at
		foundConfirm := k.GetValsetConfirm(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, val.Nonce, addr) != nil
		// if this valset has NOT been signed by operatorAddr, store it in pendingValsetReq
		// and exit the loop
		if !foundConfirm {
			pendingValsetReq = append(pendingValsetReq, *val)
		}
		// if we have more than 100 unconfirmed requests in
		// our array we should exit, TODO pagination
		if len(pendingValsetReq) > 100 {
			return true
		}
		// return false to continue the loop
		return false
	})
	return &types.QueryLastPendingValsetRequestByAddrResponse{Valsets: pendingValsetReq}, nil
}

// BatchFees queries the batch fees from unbatched pool
func (k Keeper) BatchFees(
	c context.Context,
	req *types.QueryBatchFeeRequest) (*types.QueryBatchFeeResponse, error) {
	return &types.QueryBatchFeeResponse{BatchFees: k.GetAllBatchFees(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, OutgoingTxBatchSize)}, nil
}

// LastPendingBatchRequestByAddr queries the LastPendingBatchRequestByAddr of
// the gravity module.
func (k Keeper) LastPendingBatchRequestByAddr(
	c context.Context,
	req *types.QueryLastPendingBatchRequestByAddrRequest,
) (*types.QueryLastPendingBatchRequestByAddrResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "address invalid")
	}

	var pendingBatchReq types.InternalOutgoingTxBatches

	found := false
	k.IterateOutgoingTxBatches(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, func(_ []byte, batch types.InternalOutgoingTxBatch) bool {
		foundConfirm := k.GetBatchConfirm(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, batch.BatchNonce, batch.TokenContract, addr) != nil
		if !foundConfirm {
			pendingBatchReq = append(pendingBatchReq, batch)
			found = true

			return true
		}

		return false
	})

	if found {
		ref := pendingBatchReq.ToExternalArray()
		return &types.QueryLastPendingBatchRequestByAddrResponse{Batch: ref}, nil
	}

	return &types.QueryLastPendingBatchRequestByAddrResponse{Batch: nil}, nil
}

func (k Keeper) LastPendingLogicCallByAddr(
	c context.Context,
	req *types.QueryLastPendingLogicCallByAddrRequest) (*types.QueryLastPendingLogicCallByAddrResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "address invalid")
	}

	var pendingLogicReq []types.OutgoingLogicCall
	found := false
	k.IterateOutgoingLogicCalls(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, func(_ []byte, logic types.OutgoingLogicCall) bool {
		foundConfirm := k.GetLogicCallConfirm(sdk.UnwrapSDKContext(c), req.EvmChainPrefix,
			logic.InvalidationId, logic.InvalidationNonce, addr) != nil
		if !foundConfirm {
			pendingLogicReq = append(pendingLogicReq, logic)
			found = true
			return true
		}
		return false
	})

	if found {
		return &types.QueryLastPendingLogicCallByAddrResponse{Call: pendingLogicReq}, nil
	} else {
		return &types.QueryLastPendingLogicCallByAddrResponse{Call: nil}, nil
	}
}

const MaxResults = 100 // todo: impl pagination

// OutgoingTxBatches queries the OutgoingTxBatches of the gravity module
func (k Keeper) OutgoingTxBatches(
	c context.Context,
	req *types.QueryOutgoingTxBatchesRequest) (*types.QueryOutgoingTxBatchesResponse, error) {
	var batches []types.OutgoingTxBatch
	k.IterateOutgoingTxBatches(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, func(_ []byte, batch types.InternalOutgoingTxBatch) bool {
		batches = append(batches, batch.ToExternal())
		return len(batches) == MaxResults
	})
	return &types.QueryOutgoingTxBatchesResponse{Batches: batches}, nil
}

// OutgoingLogicCalls queries the OutgoingLogicCalls of the gravity module
func (k Keeper) OutgoingLogicCalls(
	c context.Context,
	req *types.QueryOutgoingLogicCallsRequest) (*types.QueryOutgoingLogicCallsResponse, error) {
	var calls []types.OutgoingLogicCall
	k.IterateOutgoingLogicCalls(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, func(_ []byte, call types.OutgoingLogicCall) bool {
		calls = append(calls, call)
		return len(calls) == MaxResults
	})
	return &types.QueryOutgoingLogicCallsResponse{Calls: calls}, nil
}

// BatchRequestByNonce queries the BatchRequestByNonce of the gravity module.
func (k Keeper) BatchRequestByNonce(
	c context.Context,
	req *types.QueryBatchRequestByNonceRequest,
) (*types.QueryBatchRequestByNonceResponse, error) {
	addr, err := types.NewEthAddress(req.ContractAddress)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrUnknownRequest, err.Error())
	}

	foundBatch := k.GetOutgoingTxBatch(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, *addr, req.Nonce)
	if foundBatch == nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrUnknownRequest, "cannot find tx batch")
	}

	return &types.QueryBatchRequestByNonceResponse{Batch: foundBatch.ToExternal()}, nil
}

// BatchConfirms returns the batch confirmations by nonce and token contract
func (k Keeper) BatchConfirms(
	c context.Context,
	req *types.QueryBatchConfirmsRequest) (*types.QueryBatchConfirmsResponse, error) {

	var confirms []types.MsgConfirmBatch
	contract, err := types.NewEthAddress(req.ContractAddress)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid contract address in request")
	}
	k.IterateBatchConfirmByNonceAndTokenContract(sdk.UnwrapSDKContext(c), req.EvmChainPrefix,
		req.Nonce, *contract, func(_ []byte, c types.MsgConfirmBatch) bool {
			confirms = append(confirms, c)
			return false
		})
	return &types.QueryBatchConfirmsResponse{Confirms: confirms}, nil
}

// LogicConfirms returns the Logic confirmations by nonce and token contract
func (k Keeper) LogicConfirms(
	c context.Context,
	req *types.QueryLogicConfirmsRequest) (*types.QueryLogicConfirmsResponse, error) {
	confirms := k.GetLogicConfirmsByInvalidationIDAndNonce(sdk.UnwrapSDKContext(c), req.EvmChainPrefix, req.InvalidationId, req.InvalidationNonce)

	return &types.QueryLogicConfirmsResponse{Confirms: confirms}, nil
}

// LastEventNonceByAddr returns the last event nonce for the given validator address,
// this allows eth oracles to figure out where they left off
func (k Keeper) LastEventNonceByAddr(
	c context.Context,
	req *types.QueryLastEventNonceByAddrRequest) (*types.QueryLastEventNonceByAddrResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	var ret types.QueryLastEventNonceByAddrResponse
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidAddress, req.Address)
	}
	validator, found := k.GetOrchestratorValidator(ctx, addr)
	if !found {
		return nil, sdkerrors.Wrap(types.ErrUnknown, "address")
	}
	if err := sdk.VerifyAddressFormat(validator.GetOperator()); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid validator address")
	}
<<<<<<< HEAD
	lastEventNonce := k.GetLastEventNonceByValidator(ctx, req.EvmChainPrefix, validator.GetOperator())
=======
	lastEventNonce := k.GetLastEventNonceByValidator(ctx, validator.GetOperator(), types.GravityContractNonce)
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	ret.EventNonce = lastEventNonce
	return &ret, nil
}

func (k Keeper) LastERC721EventNonceByAddr(
	c context.Context, req *types.QueryLastERC721EventNonceByAddrRequest,
) (*types.QueryLastERC721EventNonceByAddrResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidAddress, req.Address)
	}

	validator, found := k.GetOrchestratorValidator(ctx, addr)
	if !found {
		return nil, sdkerrors.Wrap(types.ErrUnknown, "address")
	}
	if err := sdk.VerifyAddressFormat(validator.GetOperator()); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid validator address")
	}

	lastEventNonce := k.GetLastEventNonceByValidator(ctx, validator.GetOperator(), types.ERC721ContractNonce)

	return &types.QueryLastERC721EventNonceByAddrResponse{EventNonce: lastEventNonce}, nil
}

// DenomToERC20 queries the Cosmos Denom that maps to an Ethereum ERC20
func (k Keeper) DenomToERC20(
	c context.Context,
	req *types.QueryDenomToERC20Request) (*types.QueryDenomToERC20Response, error) {
	ctx := sdk.UnwrapSDKContext(c)
	cosmosOriginated, erc20, err := k.DenomToERC20Lookup(ctx, req.EvmChainPrefix, req.Denom)
	if err != nil {
		return nil, sdkerrors.Wrapf(err, "invalid denom (%v) queried", req.Denom)
	}
	var ret types.QueryDenomToERC20Response
	ret.Erc20 = erc20.GetAddress().Hex()
	ret.CosmosOriginated = cosmosOriginated

	return &ret, err
}

// ERC20ToDenom queries the ERC20 contract that maps to an Ethereum ERC20 if any
func (k Keeper) ERC20ToDenom(
	c context.Context,
	req *types.QueryERC20ToDenomRequest) (*types.QueryERC20ToDenomResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	ethAddr, err := types.NewEthAddress(req.Erc20)
	if err != nil {
		return nil, sdkerrors.Wrapf(err, "invalid Erc20 in request: %s", req.Erc20)
	}
	cosmosOriginated, name := k.ERC20ToDenomLookup(ctx, req.EvmChainPrefix, *ethAddr)
	var ret types.QueryERC20ToDenomResponse
	ret.Denom = name
	ret.CosmosOriginated = cosmosOriginated

	return &ret, nil
}

// GetLastObservedEthBlock queries the LastObservedEthereumBlockHeight
func (k Keeper) GetLastObservedEthBlock(
	c context.Context,
	req *types.QueryLastObservedEthBlockRequest,
) (*types.QueryLastObservedEthBlockResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	// Use the old locator pre-Mercury, when the keys changed to hashed strings
<<<<<<< HEAD
	var ethHeight types.LastObservedEthereumBlockHeight
=======
	var locator func(ctx sdk.Context, nonceSource types.NonceSource) types.LastObservedEthereumBlockHeight
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	if req.UseV1Key {
		ethHeight = k.GetOldLastObservedEthereumBlockHeight(ctx)
	} else {
		ethHeight = k.GetLastObservedEvmChainBlockHeight(ctx, req.EvmChainPrefix)
	}

<<<<<<< HEAD
=======
	ethHeight := locator(ctx, types.GravityContractNonce)

>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	return &types.QueryLastObservedEthBlockResponse{Block: ethHeight.EthereumBlockHeight}, nil
}

// GetLastObservedEthBlock queries the LastObservedEthereumBlockHeight
func (k Keeper) GetLastObservedERC721EthBlock(
	c context.Context,
	_ *types.QueryLastObservedERC721EthBlockRequest,
) (*types.QueryLastObservedERC721EthBlockResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	ethHeight := k.GetLastObservedEthereumBlockHeight(ctx, types.ERC721ContractNonce)
	return &types.QueryLastObservedERC721EthBlockResponse{Block: ethHeight.EthereumBlockHeight}, nil
}

func (k Keeper) GetOldLastObservedEthereumBlockHeight(ctx sdk.Context, _ types.NonceSource) types.LastObservedEthereumBlockHeight {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get([]byte(v1.LastObservedEthereumBlockHeightKey))

	if len(bytes) == 0 {
		return types.LastObservedEthereumBlockHeight{
			CosmosBlockHeight:   0,
			EthereumBlockHeight: 0,
		}
	}
	height := types.LastObservedEthereumBlockHeight{
		CosmosBlockHeight:   0,
		EthereumBlockHeight: 0,
	}
	k.cdc.MustUnmarshal(bytes, &height)
	return height
}

// GetLastObservedEthNonce queries the LastObservedEventNonce
func (k Keeper) GetLastObservedEthNonce(
	c context.Context,
	req *types.QueryLastObservedEthNonceRequest,
) (*types.QueryLastObservedEthNonceResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	// Use the old locator pre-Mercury, when the keys changed to hashed strings
<<<<<<< HEAD
	var nonce uint64
=======
	var locator func(ctx sdk.Context, nonceSource types.NonceSource) uint64
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	if req.UseV1Key {
		nonce = k.GetOldLastObservedEventNonce(ctx)
	} else {
		nonce = k.GetLastObservedEventNonce(ctx, req.EvmChainPrefix)
	}
<<<<<<< HEAD
=======
	nonce := locator(ctx, types.GravityContractNonce)
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770

	return &types.QueryLastObservedEthNonceResponse{Nonce: nonce}, nil
}

func (k Keeper) GetLastObservedERC721EthNonce(
	c context.Context, req *types.QueryLastObservedERC721EthNonceRequest,
) (*types.QueryLastObservedERC721EthNonceResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	nonce := k.GetLastObservedEventNonce(ctx, types.ERC721ContractNonce)

	return &types.QueryLastObservedERC721EthNonceResponse{Nonce: nonce}, nil
}

func (k Keeper) GetOldLastObservedEventNonce(ctx sdk.Context, _ types.NonceSource) uint64 {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get([]byte(v1.LastObservedEventNonceKey))

	if len(bytes) == 0 {
		return 0
	}
	return types.UInt64FromBytesUnsafe(bytes)
}

// GetAttestations queries the attestation map
func (k Keeper) GetAttestations(
	c context.Context,
	req *types.QueryAttestationsRequest,
) (*types.QueryAttestationsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	// Use the old iterator pre-Mercury, when the keys changed to hashed strings
<<<<<<< HEAD
	var iterator func(ctx sdk.Context, evmChainPrefix string, reverse bool, cb func([]byte, types.Attestation) bool)
=======
	var iterator func(ctx sdk.Context, nonceSource types.NonceSource, reverse bool, cb func([]byte, types.Attestation) bool)
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	if req.UseV1Key {
		iterator = k.IterateOldAttestations
	} else {
		iterator = k.IterateAttestations
	}

	limit := req.Limit
	if limit == 0 || limit > QUERY_ATTESTATIONS_LIMIT {
		limit = QUERY_ATTESTATIONS_LIMIT
	}

	var (
		attestations []types.Attestation
		count        uint64
		iterErr      error
	)

	reverse := strings.EqualFold(req.OrderBy, "desc")
	filter := req.Height > 0 || req.Nonce > 0 || req.ClaimType != ""

<<<<<<< HEAD
	iterator(ctx, req.EvmChainPrefix, reverse, func(_ []byte, att types.Attestation) (abort bool) {
=======

	iterator(ctx, types.GravityContractNonce, reverse, func(_ []byte, att types.Attestation) (abort bool) {
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
		claim, err := k.UnpackAttestationClaim(&att)
		if err != nil {
			iterErr = sdkerrors.Wrap(sdkerrors.ErrUnpackAny, "failed to unmarshal Ethereum claim")
			return true
		}

		var match bool
		switch {
		case filter && claim.GetEthBlockHeight() == req.Height:
			attestations = append(attestations, att)
			match = true

		case filter && claim.GetEventNonce() == req.Nonce:
			attestations = append(attestations, att)
			match = true

		case filter && claim.GetType().String() == req.ClaimType:
			attestations = append(attestations, att)
			match = true

		case !filter:
			// No filter provided, so we include the attestation. This is equivalent
			// to providing no query params or just limit and/or order_by.
			attestations = append(attestations, att)
			match = true
		}

		if match {
			count++
			if count >= limit {
				return true
			}
		}

		return false
	})
	if iterErr != nil {
		return nil, iterErr
	}

	return &types.QueryAttestationsResponse{Attestations: attestations}, nil
}

func (k Keeper) GetERC721Attestations(c context.Context, req *types.QueryERC721AttestationsRequest) (*types.QueryERC721AttestationsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	limit := req.Limit
	if limit == 0 || limit > QUERY_ATTESTATIONS_LIMIT {
		limit = QUERY_ATTESTATIONS_LIMIT
	}

	var (
		attestations []types.Attestation
		count        uint64
		iterErr      error
	)

	reverse := strings.EqualFold(req.OrderBy, "desc")
	filter := req.Height > 0 || req.Nonce > 0 || req.ClaimType != ""


	k.IterateAttestations(ctx, types.ERC721ContractNonce, reverse, func(_ []byte, att types.Attestation) (abort bool) {
		claim, err := k.UnpackAttestationClaim(&att)
		if err != nil {
			iterErr = sdkerrors.Wrap(sdkerrors.ErrUnpackAny, "failed to unmarshal Ethereum claim")
			return true
		}

		var match bool
		switch {
		case filter && claim.GetEthBlockHeight() == req.Height:
			attestations = append(attestations, att)
			match = true

		case filter && claim.GetEventNonce() == req.Nonce:
			attestations = append(attestations, att)
			match = true

		case filter && claim.GetType().String() == req.ClaimType:
			attestations = append(attestations, att)
			match = true

		case !filter:
			// No filter provided, so we include the attestation. This is equivalent
			// to providing no query params or just limit and/or order_by.
			attestations = append(attestations, att)
			match = true
		}

		if match {
			count++
			if count >= limit {
				return true
			}
		}

		return false
	})
	if iterErr != nil {
		return nil, iterErr
	}

	return &types.QueryERC721AttestationsResponse{Attestations: attestations}, nil
}

// This is the pre-Mercury Attestation iterator, which used an old prefix
<<<<<<< HEAD
// _evmChainPrefix is just for migration
func (k Keeper) IterateOldAttestations(ctx sdk.Context, _evmChainPrefix string, reverse bool, cb func([]byte, types.Attestation) bool) {
=======
func (k Keeper) IterateOldAttestations(ctx sdk.Context, _ types.NonceSource, reverse bool, cb func([]byte, types.Attestation) bool) {
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
	store := ctx.KVStore(k.storeKey)
	prefix := v1.OracleAttestationKey

	var iter storetypes.Iterator
	if reverse {
		iter = store.ReverseIterator(prefixRange([]byte(prefix)))
	} else {
		iter = store.Iterator(prefixRange([]byte(prefix)))
	}
	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		att := types.Attestation{
			Observed: false,
			Votes:    []string{},
			Height:   0,
			Claim: &codectypes.Any{
				TypeUrl:              "",
				Value:                []byte{},
				XXX_NoUnkeyedLiteral: struct{}{},
				XXX_unrecognized:     []byte{},
				XXX_sizecache:        0,
			},
		}
		k.cdc.MustUnmarshal(iter.Value(), &att)

		// cb returns true to stop early
		if cb(iter.Key(), att) {
			return
		}
	}
}

func (k Keeper) GetDelegateKeyByValidator(
	c context.Context,
	req *types.QueryDelegateKeysByValidatorAddress) (*types.QueryDelegateKeysByValidatorAddressResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	keys := k.GetDelegateKeys(ctx)
	reqValidator, err := sdk.ValAddressFromBech32(req.ValidatorAddress)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		keyValidator, err := sdk.ValAddressFromBech32(key.Validator)
		// this should be impossible due to the validate basic on the set orchestrator message
		if err != nil {
			panic("Invalid validator addr in store!")
		}
		if reqValidator.Equals(keyValidator) {
			return &types.QueryDelegateKeysByValidatorAddressResponse{EthAddress: key.EthAddress, OrchestratorAddress: key.Orchestrator}, nil
		}
	}

	return nil, sdkerrors.Wrap(types.ErrInvalid, "No validator")
}

func (k Keeper) GetDelegateKeyByOrchestrator(
	c context.Context,
	req *types.QueryDelegateKeysByOrchestratorAddress) (*types.QueryDelegateKeysByOrchestratorAddressResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	keys := k.GetDelegateKeys(ctx)
	reqOrchestrator, err := sdk.AccAddressFromBech32(req.OrchestratorAddress)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		keyOrchestrator, err := sdk.AccAddressFromBech32(key.Orchestrator)
		// this should be impossible due to the validate basic on the set orchestrator message
		if err != nil {
			panic("Invalid orchestrator addr in store!")
		}
		if reqOrchestrator.Equals(keyOrchestrator) {
			return &types.QueryDelegateKeysByOrchestratorAddressResponse{ValidatorAddress: key.Validator, EthAddress: key.EthAddress}, nil
		}

	}
	return nil, sdkerrors.Wrap(types.ErrInvalid, "No validator")
}

func (k Keeper) GetDelegateKeyByEth(
	c context.Context,
	req *types.QueryDelegateKeysByEthAddress) (*types.QueryDelegateKeysByEthAddressResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	keys := k.GetDelegateKeys(ctx)
	if err := types.ValidateEthAddress(req.EthAddress); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid eth address")
	}
	for _, key := range keys {
		if req.EthAddress == key.EthAddress {
			return &types.QueryDelegateKeysByEthAddressResponse{
				ValidatorAddress:    key.Validator,
				OrchestratorAddress: key.Orchestrator,
			}, nil
		}
	}

	return nil, sdkerrors.Wrap(types.ErrInvalid, "No validator")
}

func (k Keeper) GetPendingSendToEth(
	c context.Context,
	req *types.QueryPendingSendToEth) (*types.QueryPendingSendToEthResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	batches := k.GetOutgoingTxBatches(ctx, req.EvmChainPrefix)
	unbatchedTxs := k.GetUnbatchedTransactions(ctx, req.EvmChainPrefix)
	senderAddress := req.GetSenderAddress()
	res := types.QueryPendingSendToEthResponse{
		TransfersInBatches: []types.OutgoingTransferTx{},
		UnbatchedTransfers: []types.OutgoingTransferTx{},
	}
	for _, batch := range batches {
		for _, tx := range batch.Transactions {
			if senderAddress == "" || tx.Sender.String() == senderAddress {
				res.TransfersInBatches = append(res.TransfersInBatches, tx.ToExternal())
			}
		}
	}
	for _, tx := range unbatchedTxs {
		if senderAddress == "" || tx.Sender.String() == senderAddress {
			res.UnbatchedTransfers = append(res.UnbatchedTransfers, tx.ToExternal())
		}
	}

	return &res, nil
}

func (k Keeper) GetPendingIbcAutoForwards(
	c context.Context,
	req *types.QueryPendingIbcAutoForwards,
) (*types.QueryPendingIbcAutoForwardsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	pendingForwards := k.PendingIbcAutoForwards(ctx, req.EvmChainPrefix, req.Limit)
	return &types.QueryPendingIbcAutoForwardsResponse{PendingIbcAutoForwards: pendingForwards}, nil
}

<<<<<<< HEAD
func (k Keeper) GetListEvmChains(
	c context.Context,
	req *types.QueryListEvmChains,
) (*types.QueryListEvmChainsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	evmChains := k.GetEvmChainsWithLimit(ctx, req.Limit)
	return &types.QueryListEvmChainsResponse{EvmChains: evmChains}, nil
}

// GetMonitoredERC20Addresses formats the MonitoredERC20Tokens as strings and returns them for the grpc query
func (k Keeper) GetMonitoredERC20Addresses(
	c context.Context, req *types.QueryMonitoredERC20Addresses,
) (*types.QueryMonitoredERC20AddressesResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	addresses := k.MonitoredERC20Tokens(ctx)
	var tokenStrs []string
	for _, addr := range addresses {
		tokenStrs = append(tokenStrs, addr.GetAddress().String())
	}

	return &types.QueryMonitoredERC20AddressesResponse{Addresses: tokenStrs}, nil
}

// GetBridgeBalanceSnapshots fetches the stored BridgeBalanceSnapshots, decodes their event nonces from the store key,
// and returns them all as BridgeBalanceSnapshotResponses
func (k Keeper) GetBridgeBalanceSnapshots(
	c context.Context,
	req *types.QueryBridgeBalanceSnapshots,
) (*types.QueryBridgeBalanceSnapshotsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	limit := req.Limit
	reverse := req.NewestFirst

	snapshots := k.CollectBridgeBalanceSnapshots(ctx, reverse, limit)

	return &types.QueryBridgeBalanceSnapshotsResponse{Snapshots: snapshots}, nil
}

// GetBridgeBalanceSnapshotByEventNonce implements types.QueryServer
func (k Keeper) GetBridgeBalanceSnapshotByEventNonce(
	c context.Context,
	req *types.QueryBridgeBalanceSnapshotByEventNonce,
) (*types.QueryBridgeBalanceSnapshotByEventNonceResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)

	store := ctx.KVStore(k.storeKey)
	nonce := req.Nonce
	key := types.GetBridgeBalanceSnapshotKey(nonce, req.EvmChainPrefix)
	if !store.Has(key) {
		return nil, fmt.Errorf("no snapshot with nonce %v exists", nonce)
	}
	snapshotBz := store.Get(key)
	var snapshot types.BridgeBalanceSnapshot
	if err := k.cdc.Unmarshal(snapshotBz, &snapshot); err != nil {
		return nil, sdkerrors.Wrapf(err, "unable to fetch snapshot with nonce %v", nonce)
	}

	return &types.QueryBridgeBalanceSnapshotByEventNonceResponse{Snapshot: &snapshot}, nil
=======
func (k Keeper) GetPendingERC721IbcAutoForwards(c context.Context, req *types.QueryPendingERC721IbcAutoForwardsRequest) (*types.QueryPendingERC721IbcAutoForwardsResponse, error) {
	ctx := sdk.UnwrapSDKContext(c)
	pendingForwards := k.PendingERC721IbcAutoForwards(ctx, req.Limit)
	return &types.QueryPendingERC721IbcAutoForwardsResponse{PendingErc721IbcAutoForwards: pendingForwards}, nil
>>>>>>> 81057dc97ff3a6f3702fca99300ddbb3a7011770
}
