package keeper

import (
	"fmt"
	"strconv"

	"github.com/cosmos/cosmos-sdk/store/prefix"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/types"
)

const OutgoingTxBatchSize = 100

// BuildOutgoingTxBatch starts the following process chain:
// - find bridged denominator for given voucher type
// - determine if an unexecuted batch is already waiting for this token type, if so confirm the new batch would
// have a higher total fees. If not exit without creating a batch
// - select available transactions from the outgoing transaction pool sorted by fee desc
// - persist an outgoing batch object with an incrementing ID = nonce
// - emit an event
func (k Keeper) BuildOutgoingTxBatch(
	ctx sdk.Context,
	evmChainPrefix string,
	contract types.EthAddress,
	maxElements uint) (*types.InternalOutgoingTxBatch, error) {
	if maxElements == 0 {
		return nil, sdkerrors.Wrap(types.ErrInvalid, "max elements value")
	}
	params := k.GetParams(ctx)
	evmChainParam := params.GetEvmChain(evmChainPrefix)

	if evmChainParam == nil {
		return nil, sdkerrors.Wrap(types.ErrEmpty, "EvmChainParams not found for prefix "+evmChainPrefix)
	}

	if !evmChainParam.BridgeActive {
		return nil, sdkerrors.Wrap(types.ErrInvalid, "bridge paused")
	}

	lastBatch := k.GetLastOutgoingBatchByTokenType(ctx, evmChainPrefix, contract)

	// lastBatch may be nil if there are no existing batches, we only need
	// to perform this check if a previous batch exists
	if lastBatch != nil {
		// this traverses the current tx pool for this token type and determines what
		// fees a hypothetical batch would have if created
		currentFees := k.GetBatchFeeByTokenType(ctx, evmChainPrefix, contract, maxElements)
		if currentFees == nil {
			return nil, sdkerrors.Wrap(types.ErrInvalid, "error getting fees from tx pool")
		}

		// lastFees := lastBatch.ToExternal().GetFees()
		// if lastFees.GTE(currentFees.TotalFees) {
		// 	return nil, sdkerrors.Wrap(types.ErrInvalid, "new batch would not be more profitable")
		// }
	}

	selectedTxs, err := k.pickUnbatchedTxs(ctx, evmChainPrefix, contract, maxElements)
	if err != nil {
		return nil, err
	} else if len(selectedTxs) == 0 {
		return nil, sdkerrors.Wrap(types.ErrInvalid, "no transactions of this type to batch")
	}

	nextID := k.autoIncrementID(ctx, types.AppendChainPrefix(types.KeyLastOutgoingBatchID, evmChainPrefix))
	batch, err := types.NewInternalOutgingTxBatch(nextID, k.getBatchTimeoutHeight(ctx, evmChainPrefix), selectedTxs, contract, 0)
	if err != nil {
		panic(sdkerrors.Wrap(err, "unable to create batch"))
	}
	// set the current block height when storing the batch
	batch.CosmosBlockCreated = uint64(ctx.BlockHeight())
	k.StoreBatch(ctx, evmChainPrefix, *batch)

	// Get the checkpoint and store it as a legit past batch
	checkpoint := batch.GetCheckpoint(k.GetGravityID(ctx, evmChainPrefix))
	k.SetPastEthSignatureCheckpoint(ctx, evmChainPrefix, checkpoint)

	ctx.EventManager().EmitTypedEvent(
		&types.EventOutgoingBatch{
			BridgeContract: k.GetBridgeContractAddress(ctx, evmChainPrefix).GetAddress().Hex(),
			BridgeChainId:  strconv.Itoa(int(k.GetBridgeChainID(ctx, evmChainPrefix))),
			BatchId:        string(types.GetOutgoingTxBatchKey(evmChainPrefix, contract, nextID)),
			Nonce:          fmt.Sprint(nextID),
		},
	)
	return batch, nil
}

// This gets the batch timeout height in evm chain blocks.
func (k Keeper) getBatchTimeoutHeight(ctx sdk.Context, evmChainPrefix string) uint64 {
	params := k.GetParams(ctx)

	evmChainParam := params.GetEvmChain(evmChainPrefix)

	if evmChainParam == nil {
		return 0
	}

	currentCosmosHeight := ctx.BlockHeight()
	// we store the last observed Cosmos and evm chain heights, we do not concern ourselves if these values are zero because
	// no batch can be produced if the last evm chain block height is not first populated by a deposit event.
	heights := k.GetLastObservedEvmChainBlockHeight(ctx, evmChainPrefix)
	if heights.CosmosBlockHeight == 0 || heights.EthereumBlockHeight == 0 {
		return 0
	}
	// we project how long it has been in milliseconds since the last evm chain block height was observed
	projectedMillis := (uint64(currentCosmosHeight) - heights.CosmosBlockHeight) * params.AverageBlockTime
	// we convert that projection into the current evm chain height using the average evm chain block time in millis
	projectedCurrentEvmChainHeight := (projectedMillis / evmChainParam.AverageEthereumBlockTime) + heights.EthereumBlockHeight
	// we convert our target time for block timeouts (lets say 12 hours) into a number of blocks to
	// place on top of our projection of the current evm chain block height.
	blocksToAdd := params.TargetBatchTimeout / evmChainParam.AverageEthereumBlockTime
	return projectedCurrentEvmChainHeight + blocksToAdd
}

// OutgoingTxBatchExecuted is run when the Cosmos chain detects that a batch has been executed on evm chain
// It frees all the transactions in the batch, then cancels all earlier batches, this function panics instead
// of returning errors because any failure will cause a double spend.
func (k Keeper) OutgoingTxBatchExecuted(ctx sdk.Context, tokenContract types.EthAddress, claim types.MsgBatchSendToEthClaim) {
	b := k.GetOutgoingTxBatch(ctx, claim.EvmChainPrefix, tokenContract, claim.BatchNonce)
	if b == nil {
		panic(fmt.Sprintf("unknown batch nonce for outgoing tx batch %s %d", tokenContract.GetAddress().Hex(), claim.BatchNonce))
	}
	if b.BatchTimeout <= claim.EthBlockHeight {
		panic(fmt.Sprintf("Batch with nonce %d submitted after it timed out (submission %d >= timeout %d)?", claim.BatchNonce, claim.EthBlockHeight, b.BatchTimeout))
	}
	contract := b.TokenContract
	// Burn tokens if they're evm chain originated
	if isCosmosOriginated, _ := k.ERC20ToDenomLookup(ctx, claim.EvmChainPrefix, contract); !isCosmosOriginated {
		totalToBurn := sdk.NewInt(0)
		for _, tx := range b.Transactions {
			totalToBurn = totalToBurn.Add(tx.Erc20Token.Amount.Add(tx.Erc20Fee.Amount))
		}
		// burn vouchers to send them back to evm chain
		erc20, err := types.NewInternalERC20Token(totalToBurn, contract.GetAddress().Hex())
		if err != nil {
			panic(sdkerrors.Wrapf(err, "invalid ERC20 address in executed batch"))
		}
		burnVouchers := sdk.NewCoins(erc20.GravityCoin(claim.EvmChainPrefix))
		if err := k.bankKeeper.BurnCoins(ctx, types.ModuleName, burnVouchers); err != nil {
			panic(err)
		}
	}

	// Iterate through remaining batches
	k.IterateOutgoingTxBatches(ctx, claim.EvmChainPrefix, func(key []byte, batch types.InternalOutgoingTxBatch) bool {
		// If the iterated batches nonce is lower than the one that was just executed, cancel it
		if batch.BatchNonce < b.BatchNonce && batch.TokenContract.GetAddress() == tokenContract.GetAddress() {
			err := k.CancelOutgoingTxBatch(ctx, claim.EvmChainPrefix, tokenContract, batch.BatchNonce)
			if err != nil {
				panic(fmt.Sprintf("Failed cancel out batch %s %d while trying to execute %s %d with %s",
					tokenContract.GetAddress().Hex(), batch.BatchNonce,
					tokenContract.GetAddress().Hex(), claim.BatchNonce, err))
			}
		}
		return false
	})

	// Delete batch since it is finished
	k.DeleteBatch(ctx, claim.EvmChainPrefix, *b)
	// Delete it's confirmations as well
	k.DeleteBatchConfirms(ctx, claim.EvmChainPrefix, *b)
}

// StoreBatch stores a transaction batch, it will refuse to overwrite an existing
// batch and panic instead, once a batch is stored in state signature collection begins
// so no mutation of a batch in state can ever be valid
func (k Keeper) StoreBatch(ctx sdk.Context, evmChainPrefix string, batch types.InternalOutgoingTxBatch) {
	if err := batch.ValidateBasic(); err != nil {
		panic(sdkerrors.Wrap(err, "attempted to store invalid batch"))
	}
	externalBatch := batch.ToExternal()
	store := ctx.KVStore(k.storeKey)
	key := types.GetOutgoingTxBatchKey(evmChainPrefix, batch.TokenContract, batch.BatchNonce)
	if store.Has(key) {
		panic(sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "Should never overwrite batch!"))
	}
	store.Set(key, k.cdc.MustMarshal(&externalBatch))
}

// DeleteBatch deletes an outgoing transaction batch
func (k Keeper) DeleteBatch(ctx sdk.Context, evmChainPrefix string, batch types.InternalOutgoingTxBatch) {
	if err := batch.ValidateBasic(); err != nil {
		panic(sdkerrors.Wrap(err, "attempted to delete invalid batch"))
	}
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.GetOutgoingTxBatchKey(evmChainPrefix, batch.TokenContract, batch.BatchNonce))
}

// pickUnbatchedTxs moves unbatched Txs from the pool into a collection ready for batching
func (k Keeper) pickUnbatchedTxs(
	ctx sdk.Context,
	evmChainPrefix string,
	contractAddress types.EthAddress,
	maxElements uint) ([]*types.InternalOutgoingTransferTx, error) {
	var selectedTxs []*types.InternalOutgoingTransferTx
	var err error
	k.IterateUnbatchedTransactionsByContract(ctx, evmChainPrefix, contractAddress, func(_ []byte, tx *types.InternalOutgoingTransferTx) bool {
		if tx != nil && tx.Erc20Fee != nil {
			// check the blacklist before picking this tx, this was already
			// checked on MsgSendToEth, but we want to double check. For example
			// a major erc20 throws on send to address X a MsgSendToEth is made with that destination
			// batches with that tx will forever panic, blocking that erc20. With this check governance
			// can add that address to the blacklist and quickly eliminate the issue. Note this is
			// very inefficient, IsOnBlacklist is O(blacklist-length) and should be made faster
			if !k.IsOnBlacklist(ctx, evmChainPrefix, *tx.DestAddress) {
				selectedTxs = append(selectedTxs, tx)
				err = k.removeUnbatchedTX(ctx, evmChainPrefix, *tx.Erc20Fee, tx.Id)
				if err != nil {
					panic("Failed to remote tx from unbatched queue")
				}

				// double check that no duplicates exist in the index
				oldTx, oldTxErr := k.GetUnbatchedTxByFeeAndId(ctx, evmChainPrefix, *tx.Erc20Fee, tx.Id)
				if oldTx != nil || oldTxErr == nil {
					panic("picked a duplicate transaction from the pool, duplicates should never exist!")
				}

				return uint(len(selectedTxs)) == maxElements
			} else {
				// if the tx was on the blacklist we return false
				// to continue to the next loop iteration
				return false
			}
		} else {
			panic("tx and fee should never be nil!")
		}
	})
	return selectedTxs, err
}

// GetOutgoingTxBatch loads a batch object. Returns nil when not exists.
func (k Keeper) GetOutgoingTxBatch(ctx sdk.Context, evmChainPrefix string, tokenContract types.EthAddress, nonce uint64) *types.InternalOutgoingTxBatch {
	store := ctx.KVStore(k.storeKey)
	key := types.GetOutgoingTxBatchKey(evmChainPrefix, tokenContract, nonce)
	bz := store.Get(key)
	if len(bz) == 0 {
		return nil
	}
	var b types.OutgoingTxBatch
	k.cdc.MustUnmarshal(bz, &b)
	for _, tx := range b.Transactions {
		tx.Erc20Token.Contract = tokenContract.GetAddress().Hex()
		tx.Erc20Fee.Contract = tokenContract.GetAddress().Hex()
	}
	ret, err := b.ToInternal()
	if err != nil {
		panic(sdkerrors.Wrap(err, "found invalid batch in store"))
	}
	return ret
}

// CancelOutgoingTxBatch releases all TX in the batch and deletes the batch
func (k Keeper) CancelOutgoingTxBatch(ctx sdk.Context, evmChainPrefix string, tokenContract types.EthAddress, nonce uint64) error {
	batch := k.GetOutgoingTxBatch(ctx, evmChainPrefix, tokenContract, nonce)
	if batch == nil {
		return types.ErrUnknown
	}
	for _, tx := range batch.Transactions {
		err := k.addUnbatchedTX(ctx, evmChainPrefix, tx)
		if err != nil {
			panic(sdkerrors.Wrapf(err, "unable to add batched transaction back into pool %v", tx))
		}
	}

	// Delete batch since it is finished
	k.DeleteBatch(ctx, evmChainPrefix, *batch)
	// Delete it's confirmations as well
	k.DeleteBatchConfirms(ctx, evmChainPrefix, *batch)

	ctx.EventManager().EmitTypedEvent(
		&types.EventOutgoingBatchCanceled{
			BridgeContract: k.GetBridgeContractAddress(ctx, evmChainPrefix).GetAddress().Hex(),
			BridgeChainId:  strconv.Itoa(int(k.GetBridgeChainID(ctx, evmChainPrefix))),
			BatchId:        string(types.GetOutgoingTxBatchKey(evmChainPrefix, tokenContract, nonce)),
			Nonce:          fmt.Sprint(nonce),
		},
	)
	return nil
}

// IterateOutgoingTxBatches iterates through all outgoing batches in DESC order.
func (k Keeper) IterateOutgoingTxBatches(ctx sdk.Context, evmChainPrefix string, cb func(key []byte, batch types.InternalOutgoingTxBatch) bool) {
	prefixStore := prefix.NewStore(ctx.KVStore(k.storeKey), types.AppendChainPrefix(types.OutgoingTxBatchKey, evmChainPrefix))
	iter := prefixStore.ReverseIterator(nil, nil)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		var batch types.OutgoingTxBatch
		k.cdc.MustUnmarshal(iter.Value(), &batch)
		intBatch, err := batch.ToInternal()
		if err != nil || intBatch == nil {
			panic(sdkerrors.Wrap(err, "found invalid batch in store"))
		}
		// cb returns true to stop early
		if cb(iter.Key(), *intBatch) {
			break
		}
	}
}

// GetOutgoingTxBatches returns the outgoing tx batches for specific evm chain
func (k Keeper) GetOutgoingTxBatches(ctx sdk.Context, evmChainPrefix string) (out []types.InternalOutgoingTxBatch) {
	k.IterateOutgoingTxBatches(ctx, evmChainPrefix, func(_ []byte, batch types.InternalOutgoingTxBatch) bool {
		out = append(out, batch)
		return false
	})
	return
}

func (k Keeper) GetOutgoingTxBatchesByNonce(ctx sdk.Context, evmChainPrefix string) map[uint64]types.InternalOutgoingTxBatch {
	batchesByNonce := make(map[uint64]types.InternalOutgoingTxBatch)
	k.IterateOutgoingTxBatches(ctx, evmChainPrefix, func(_ []byte, batch types.InternalOutgoingTxBatch) bool {
		if _, exists := batchesByNonce[batch.BatchNonce]; exists {
			panic(fmt.Sprintf("Batch with duplicate batch nonce %d in store", batch.BatchNonce))
		}
		batchesByNonce[batch.BatchNonce] = batch
		return false
	})
	return batchesByNonce
}

// GetLastOutgoingBatchByTokenType gets the latest outgoing tx batch by token type for specific evm chain
func (k Keeper) GetLastOutgoingBatchByTokenType(ctx sdk.Context, evmChainPrefix string, token types.EthAddress) *types.InternalOutgoingTxBatch {
	batches := k.GetOutgoingTxBatches(ctx, evmChainPrefix)
	var lastBatch *types.InternalOutgoingTxBatch = nil
	lastNonce := uint64(0)
	for i, batch := range batches {
		if batch.TokenContract.GetAddress() == token.GetAddress() && batch.BatchNonce > lastNonce {
			lastBatch = &batches[i]
			lastNonce = batch.BatchNonce
		}
	}
	return lastBatch
}

// HasLastSlashedBatchBlock returns true if the last slashed batch block has been set in the store
func (k Keeper) HasLastSlashedBatchBlock(ctx sdk.Context, evmChainPrefix string) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(types.AppendChainPrefix(types.LastSlashedBatchBlock, evmChainPrefix))
}

// SetLastSlashedBatchBlock sets the latest slashed Batch block height this is done by
// block height instead of nonce because batches could have individual nonces for each token type
// this function will panic if a lower last slashed block is set, this protects against programmer error
func (k Keeper) SetLastSlashedBatchBlock(ctx sdk.Context, evmChainPrefix string, blockHeight uint64) {

	if k.HasLastSlashedBatchBlock(ctx, evmChainPrefix) && k.GetLastSlashedBatchBlock(ctx, evmChainPrefix) > blockHeight {
		panic("Attempted to decrement LastSlashedBatchBlock")
	}

	store := ctx.KVStore(k.storeKey)
	store.Set(types.AppendChainPrefix(types.LastSlashedBatchBlock, evmChainPrefix), types.UInt64Bytes(blockHeight))
}

// GetLastSlashedBatchBlock returns the latest slashed Batch block
func (k Keeper) GetLastSlashedBatchBlock(ctx sdk.Context, evmChainPrefix string) uint64 {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.AppendChainPrefix(types.LastSlashedBatchBlock, evmChainPrefix))

	if len(bytes) == 0 {
		panic("Last slashed batch block not initialized from genesis")
	}
	return types.UInt64FromBytesUnsafe(bytes)
}

// GetUnSlashedBatches returns all the unslashed batches in state
func (k Keeper) GetUnSlashedBatches(ctx sdk.Context, evmChainPrefix string, maxHeight uint64) (out []types.InternalOutgoingTxBatch) {
	lastSlashedBatchBlock := k.GetLastSlashedBatchBlock(ctx, evmChainPrefix)
	batches := k.GetOutgoingTxBatches(ctx, evmChainPrefix)
	for _, batch := range batches {
		if batch.CosmosBlockCreated > lastSlashedBatchBlock && batch.CosmosBlockCreated < maxHeight {
			out = append(out, batch)
		}
	}
	return
}
