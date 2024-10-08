package keeper

import (
	"fmt"
	"sort"
	"strconv"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/types"
	"github.com/cosmos/cosmos-sdk/store/prefix"
)

// TODO-JT: carefully look at atomicity of this function
func (k Keeper) Attest(
	ctx sdk.Context,
	claim types.EthereumClaim,
	anyClaim *codectypes.Any,
) (*types.Attestation, error) {
	val, found := k.GetOrchestratorValidator(ctx, claim.GetClaimer())
	if !found {
		panic("Could not find ValAddr for delegate key, should be checked by now")
	}
	valAddr := val.GetOperator()
	if err := sdk.VerifyAddressFormat(valAddr); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid orchestrator validator address")
	}
	// Check that the nonce of this event is exactly one higher than the last nonce stored by this validator.
	// We check the event nonce in processAttestation as well,
	// but checking it here gives individual eth signers a chance to retry,
	// and prevents validators from submitting two claims with the same nonce.
	// This prevents there being two attestations with the same nonce that get 2/3s of the votes
	// in the endBlocker.
	lastEventNonce := k.GetLastEventNonceByValidator(ctx, claim.GetEvmChainPrefix(), valAddr)
	if claim.GetEventNonce() != lastEventNonce+1 {
		return nil, fmt.Errorf(types.ErrNonContiguousEventNonce.Error(), lastEventNonce+1, claim.GetEventNonce())
	}

	// Tries to get an attestation with the same eventNonce and claim as the claim that was submitted.
	hash, err := claim.ClaimHash()
	if err != nil {
		return nil, sdkerrors.Wrap(err, "unable to compute claim hash")
	}
	att := k.GetAttestation(ctx, claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash)

	// If it does not exist, create a new one.
	if att == nil {
		att = &types.Attestation{
			Observed: false,
			Votes:    []string{},
			Height:   uint64(ctx.BlockHeight()),
			Claim:    anyClaim,
		}
	}

	ethClaim, err := k.UnpackAttestationClaim(att)
	if err != nil {
		panic(fmt.Sprintf("could not unpack stored attestation claim, %v", err))
	}

	if ethClaim.GetEthBlockHeight() == claim.GetEthBlockHeight() {

		// Add the validator's vote to this attestation
		att.Votes = append(att.Votes, valAddr.String())

		k.SetAttestation(ctx, claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash, att)
		k.SetLastEventNonceByValidator(ctx, claim.GetEvmChainPrefix(), valAddr, claim.GetEventNonce())

		return att, nil
	} else {
		return nil, fmt.Errorf("invalid height - this claim's height is %v while the stored height is %v", claim.GetEthBlockHeight(), ethClaim.GetEthBlockHeight())
	}
}

// TryAttestation checks if an attestation has enough votes to be applied to the consensus state
// and has not already been marked Observed, then calls processAttestation to actually apply it to the state,
// and then marks it Observed and emits an event.
func (k Keeper) TryAttestation(ctx sdk.Context, att *types.Attestation) {

	claim, err := k.UnpackAttestationClaim(att)
	if err != nil {
		panic("could not cast to claim")
	}
	hash, err := claim.ClaimHash()
	if err != nil {
		panic("unable to compute claim hash")
	}
	// If the attestation has not yet been Observed, sum up the votes and see if it is ready to apply to the state.
	// This conditional stops the attestation from accidentally being applied twice.
	if !att.Observed {
		// Sum the current powers of all validators who have voted and see if it passes the current threshold
		// TODO: The different integer types and math here needs a careful review
		totalPower := k.StakingKeeper.GetLastTotalPower(ctx)
		requiredPower := types.AttestationVotesPowerThreshold.Mul(totalPower).Quo(sdk.NewInt(100))
		attestationPower := sdk.NewInt(0)
		for _, validator := range att.Votes {
			val, err := sdk.ValAddressFromBech32(validator)
			if err != nil {
				panic(err)
			}
			validatorPower := k.StakingKeeper.GetLastValidatorPower(ctx, val)
			// Add it to the attestation power's sum
			attestationPower = attestationPower.Add(sdk.NewInt(validatorPower))
			// If the power of all the validators that have voted on the attestation is higher or equal to the threshold,
			// process the attestation, set Observed to true, and break
			if attestationPower.GTE(requiredPower) {
				lastEventNonce := k.GetLastObservedEventNonce(ctx, claim.GetEvmChainPrefix())
				// this check is performed at the next level up so this should never panic
				// outside of programmer error.
				if claim.GetEventNonce() != lastEventNonce+1 {
					panic("attempting to apply events to state out of order")
				}

				k.setLastObservedEventNonce(ctx, claim.GetEvmChainPrefix(), claim.GetEventNonce())
				k.SetLastObservedEvmChainBlockHeight(ctx, claim.GetEvmChainPrefix(), claim.GetEthBlockHeight())

				att.Observed = true
				k.SetAttestation(ctx, claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash, att)

				expectedSupplyChange, err := k.ExpectedSupplyChange(ctx, claim)
				if err != nil || expectedSupplyChange == nil {
					errMsg := fmt.Sprintf("error calculating change to bank supply due to attestation: %v", err)
					k.logger(ctx).Error(errMsg)
					panic(errMsg)
				}
				k.processAttestation(ctx, att, claim)
				k.emitObservedEvent(ctx, att, claim)

				// Add a new bridge balance to the store, check the supply of all monitored erc20 tokens too
				k.updateBridgeBalanceSnapshots(ctx, claim, *expectedSupplyChange)

				break
			}
		}
	} else {
		// We panic here because this should never happen
		panic("attempting to process observed attestation")
	}
}

// processAttestation actually applies the attestation to the consensus state
func (k Keeper) processAttestation(ctx sdk.Context, att *types.Attestation, claim types.EthereumClaim) {
	hash, err := claim.ClaimHash()
	if err != nil {
		panic("unable to compute claim hash")
	}
	// then execute in a new Tx so that we can store state on failure
	xCtx, commit := ctx.CacheContext()
	if err := k.AttestationHandler.Handle(xCtx, *att, claim); err != nil { // execute with a transient storage
		// If the attestation fails, something has gone wrong and we can't recover it. Log and move on
		// The attestation will still be marked "Observed", allowing the oracle to progress properly
		k.logger(ctx).Error("attestation failed",
			"cause", err.Error(),
			"claim type", claim.GetType(),
			"id", types.GetAttestationKey(claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash),
			"nonce", fmt.Sprint(claim.GetEventNonce()),
		)
	} else {
		commit() // persist transient storage
	}
}

// emitObservedEvent emits an event with information about an attestation that has been applied to
// consensus state.
func (k Keeper) emitObservedEvent(ctx sdk.Context, att *types.Attestation, claim types.EthereumClaim) {
	hash, err := claim.ClaimHash()
	if err != nil {
		panic(sdkerrors.Wrap(err, "unable to compute claim hash"))
	}

	err = ctx.EventManager().EmitTypedEvent(
		&types.EventObservation{
			AttestationType: string(claim.GetType()),
			BridgeContract:  k.GetBridgeContractAddress(ctx, claim.GetEvmChainPrefix()).GetAddress().Hex(),
			BridgeChainId:   strconv.Itoa(int(k.GetBridgeChainID(ctx, claim.GetEvmChainPrefix()))),
			AttestationId:   string(types.GetAttestationKey(claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash)),
			Nonce:           fmt.Sprint(claim.GetEventNonce()),
		},
	)
	if err != nil {
		panic(err)
	}
}

// SetAttestation sets the attestation in the store
func (k Keeper) SetAttestation(ctx sdk.Context, evmChainPrefix string, eventNonce uint64, claimHash []byte, att *types.Attestation) {
	store := ctx.KVStore(k.storeKey)
	aKey := types.GetAttestationKey(evmChainPrefix, eventNonce, claimHash)
	store.Set(aKey, k.cdc.MustMarshal(att))
}

// GetAttestation return an attestation given a nonce
func (k Keeper) GetAttestation(ctx sdk.Context, evmChainPrefix string, eventNonce uint64, claimHash []byte) *types.Attestation {
	store := ctx.KVStore(k.storeKey)
	aKey := types.GetAttestationKey(evmChainPrefix, eventNonce, claimHash)
	bz := store.Get(aKey)
	if len(bz) == 0 {
		return nil
	}
	var att types.Attestation
	k.cdc.MustUnmarshal(bz, &att)
	return &att
}

// DeleteAttestation deletes the given attestation
func (k Keeper) DeleteAttestation(ctx sdk.Context, att types.Attestation) {
	claim, err := k.UnpackAttestationClaim(&att)
	if err != nil {
		panic("Bad Attestation in DeleteAttestation")
	}
	hash, err := claim.ClaimHash()
	if err != nil {
		panic(sdkerrors.Wrap(err, "unable to compute claim hash"))
	}
	store := ctx.KVStore(k.storeKey)

	store.Delete(types.GetAttestationKey(claim.GetEvmChainPrefix(), claim.GetEventNonce(), hash))
}

// GetAttestationMapping returns a mapping of eventnonce -> attestations at that nonce
// it also returns a pre-sorted array of the keys, this assists callers of this function
// by providing a deterministic iteration order. You should always iterate over ordered keys
// if you are iterating this map at all.
func (k Keeper) GetAttestationMapping(ctx sdk.Context, evmChainPrefix string) (attestationMapping map[uint64][]types.Attestation, orderedKeys []uint64) {
	attestationMapping = make(map[uint64][]types.Attestation)
	k.IterateAttestations(ctx, evmChainPrefix, false, func(_ []byte, att types.Attestation) bool {
		claim, err := k.UnpackAttestationClaim(&att)
		if err != nil {
			panic("couldn't cast to claim")
		}

		if val, ok := attestationMapping[claim.GetEventNonce()]; !ok {
			attestationMapping[claim.GetEventNonce()] = []types.Attestation{att}
		} else {
			attestationMapping[claim.GetEventNonce()] = append(val, att)
		}
		return false
	})
	orderedKeys = make([]uint64, 0, len(attestationMapping))
	for k := range attestationMapping {
		orderedKeys = append(orderedKeys, k)
	}
	sort.Slice(orderedKeys, func(i, j int) bool { return orderedKeys[i] < orderedKeys[j] })

	return
}

// IterateAttestations iterates through all attestations executing a given callback on each discovered attestation
// If reverse is true, attestations will be returned in descending order by key (aka by event nonce and then claim hash)
// cb should return true to stop iteration, false to continue
func (k Keeper) IterateAttestations(ctx sdk.Context, evmChainPrefix string, reverse bool, cb func([]byte, types.Attestation) bool) {
	store := ctx.KVStore(k.storeKey)
	keyPrefix := types.AppendChainPrefix(types.OracleAttestationKey, evmChainPrefix)

	var iter storetypes.Iterator
	if reverse {
		iter = store.ReverseIterator(prefixRange(keyPrefix))
	} else {
		iter = store.Iterator(prefixRange(keyPrefix))
	}
	defer func(iter storetypes.Iterator) {
		err := iter.Close()
		if err != nil {
			panic("Unable to close attestation iterator!")
		}
	}(iter)

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

// IterateClaims iterates through all attestations, filtering them for claims of a given type
// If reverse is true, attestations will be returned in descending order by key (aka by event nonce and then claim hash)
// cb should return true to stop iteration, false to continue
func (k Keeper) IterateClaims(ctx sdk.Context, evmChainPrefix string, reverse bool, claimType types.ClaimType, cb func(key []byte, att types.Attestation, claim types.EthereumClaim) (stop bool)) {
	typeUrl := types.ClaimTypeToTypeUrl(claimType) // Used to avoid unpacking undesired attestations

	k.IterateAttestations(ctx, evmChainPrefix, reverse, func(key []byte, att types.Attestation) bool {
		if att.Claim.TypeUrl == typeUrl {
			claim, err := k.UnpackAttestationClaim(&att)
			if err != nil {
				panic(fmt.Sprintf("Discovered invalid claim in attestation %v under key %v: %v", att, key, err))
			}

			return cb(key, att, claim)
		}
		return false
	})
}

// GetMostRecentAttestations returns sorted (by nonce) attestations up to a provided limit number of attestations
// Note: calls GetAttestationMapping in the hopes that there are potentially many attestations
// which are distributed between few nonces to minimize sorting time
func (k Keeper) GetMostRecentAttestations(ctx sdk.Context, evmChainPrefix string, limit uint64) []types.Attestation {
	attestationMapping, keys := k.GetAttestationMapping(ctx, evmChainPrefix)
	attestations := make([]types.Attestation, 0, limit)

	// Iterate the nonces and collect the attestations
	count := 0
	for _, nonce := range keys {
		if count >= int(limit) {
			break
		}
		for _, att := range attestationMapping[nonce] {
			if count >= int(limit) {
				break
			}
			attestations = append(attestations, att)
			count++
		}
	}

	return attestations
}

// GetLastObservedEventNonce returns the latest observed event nonce
func (k Keeper) GetLastObservedEventNonce(ctx sdk.Context, evmChainPrefix string) uint64 {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.AppendChainPrefix(types.LastObservedEventNonceKey, evmChainPrefix))

	if len(bytes) == 0 {
		return 0
	}
	if len(bytes) > 8 {
		panic("Last observed event nonce is not a uint64!")
	}

	return types.UInt64FromBytesUnsafe(bytes)
}

// GetLastObservedEvmChainBlockHeight height gets the block height to of the last observed attestation from
// the store
func (k Keeper) GetLastObservedEvmChainBlockHeight(ctx sdk.Context, evmChainPrefix string) types.LastObservedEthereumBlockHeight {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.AppendChainPrefix(types.LastObservedEvmBlockHeightKey, evmChainPrefix))
	height := types.LastObservedEthereumBlockHeight{
		CosmosBlockHeight:   0,
		EthereumBlockHeight: 0,
	}

	if len(bytes) > 0 {
		k.cdc.MustUnmarshal(bytes, &height)
	}

	return height
}

// SetLastObservedEvmChainBlockHeight sets the block height in the store.
func (k Keeper) SetLastObservedEvmChainBlockHeight(ctx sdk.Context, evmChainPrefix string, evmChainHeight uint64) {
	store := ctx.KVStore(k.storeKey)
	previous := k.GetLastObservedEvmChainBlockHeight(ctx, evmChainPrefix)
	if previous.EthereumBlockHeight > evmChainHeight {
		panic("Attempt to roll back Ethereum block height!")
	}
	height := types.LastObservedEthereumBlockHeight{
		EthereumBlockHeight: evmChainHeight,
		CosmosBlockHeight:   uint64(ctx.BlockHeight()),
	}
	store.Set(types.AppendChainPrefix(types.LastObservedEvmBlockHeightKey, evmChainPrefix), k.cdc.MustMarshal(&height))
}

// GetLastObservedValset retrieves the last observed validator set from the store
// WARNING: This value is not an up to date validator set on evm chain, it is a validator set
// that AT ONE POINT was the one in the Gravity bridge on evm chain. If you assume that it's up
// to date you may break the bridge
func (k Keeper) GetLastObservedValset(ctx sdk.Context, evmChainPrefix string) *types.Valset {
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.AppendChainPrefix(types.LastObservedValsetKey, evmChainPrefix))

	if len(bytes) == 0 {
		return nil
	}
	valset := types.Valset{
		Nonce:        0,
		Members:      []types.BridgeValidator{},
		Height:       0,
		RewardAmount: sdk.Int{},
		RewardToken:  "",
	}
	k.cdc.MustUnmarshal(bytes, &valset)
	return &valset
}

// SetLastObservedValset updates the last observed validator set in the store
func (k Keeper) SetLastObservedValset(ctx sdk.Context, evmChainPrefix string, valset types.Valset) {
	store := ctx.KVStore(k.storeKey)
	store.Set(types.AppendChainPrefix(types.LastObservedValsetKey, evmChainPrefix), k.cdc.MustMarshal(&valset))
}

// setLastObservedEventNonce sets the latest observed event nonce
func (k Keeper) setLastObservedEventNonce(ctx sdk.Context, evmChainPrefix string, nonce uint64) {
	store := ctx.KVStore(k.storeKey)
	last := k.GetLastObservedEventNonce(ctx, evmChainPrefix)
	// event nonce must increase, unless it's zero at which point allow zero to be set
	// as many times as needed (genesis test setup etc)
	zeroCase := last == 0 && nonce == 0
	if last >= nonce && !zeroCase {
		panic("Event nonce going backwards or replay!")
	}
	store.Set(types.AppendChainPrefix(types.LastObservedEventNonceKey, evmChainPrefix), types.UInt64Bytes(nonce))
}

// GetLastEventNonceByValidator returns the latest event nonce for a given validator
func (k Keeper) GetLastEventNonceByValidator(ctx sdk.Context, evmChainPrefix string, validator sdk.ValAddress) uint64 {
	if err := sdk.VerifyAddressFormat(validator); err != nil {
		panic(sdkerrors.Wrap(err, "invalid validator address"))
	}
	store := ctx.KVStore(k.storeKey)
	bytes := store.Get(types.GetLastEventNonceByValidatorKey(evmChainPrefix, validator))

	if len(bytes) == 0 {
		// in the case that we have no existing value this is the first
		// time a validator is submitting a claim. Since we don't want to force
		// them to replay the entire history of all events ever we can't start
		// at zero
		lastEventNonce := k.GetLastObservedEventNonce(ctx, evmChainPrefix)
		if lastEventNonce >= 1 {
			return lastEventNonce - 1
		} else {
			return 0
		}
	}
	return types.UInt64FromBytesUnsafe(bytes)
}

// setLastEventNonceByValidator sets the latest event nonce for a give validator
func (k Keeper) SetLastEventNonceByValidator(ctx sdk.Context, evmChainPrefix string, validator sdk.ValAddress, nonce uint64) {
	if err := sdk.VerifyAddressFormat(validator); err != nil {
		panic(sdkerrors.Wrap(err, "invalid validator address"))
	}
	store := ctx.KVStore(k.storeKey)
	store.Set(types.GetLastEventNonceByValidatorKey(evmChainPrefix, validator), types.UInt64Bytes(nonce))
}

// IterateValidatorLastEventNonces iterates through all batch confirmations
func (k Keeper) IterateValidatorLastEventNonces(ctx sdk.Context, evmChainPrefix string, cb func(key []byte, nonce uint64) (stop bool)) {
	store := ctx.KVStore(k.storeKey)
	prefixStore := prefix.NewStore(store, types.AppendChainPrefix(types.LastEventNonceByValidatorKey, evmChainPrefix))
	iter := prefixStore.Iterator(nil, nil)

	defer iter.Close()

	for ; iter.Valid(); iter.Next() {
		nonce := types.UInt64FromBytesUnsafe(iter.Value())

		// cb returns true to stop early
		if cb(iter.Key(), nonce) {
			break
		}
	}
}

// ExpectedSupplyChange calculates the expected change to the bank supply as a result
// of this attestation's application to state. E.g. a MsgSendToCosmosClaim would increase supply,
// while MsgBatchSendToEthClaim would decrease supply.
// Note: This MUST be called before applying the attestation, since batches are deleted
// immediately after processing batch claims.
func (k Keeper) ExpectedSupplyChange(ctx sdk.Context, ethClaim types.EthereumClaim) (*sdk.Coins, error) {
	var change sdk.Coins
	switch ethClaim.GetType() {
	// Send to Cosmos
	case types.CLAIM_TYPE_SEND_TO_COSMOS:
		var claim *types.MsgSendToCosmosClaim = (ethClaim).(*types.MsgSendToCosmosClaim)
		contract, err := types.NewEthAddress(claim.TokenContract)
		if err != nil {
			return nil, fmt.Errorf("attestation contains claim with invalid contract (%v): %v", claim.TokenContract, err)
		}
		change = sdk.Coins{sdk.Coin{Denom: types.GravityDenom(claim.EvmChainPrefix, *contract), Amount: claim.Amount}}

	// Batch Send to Eth
	case types.CLAIM_TYPE_BATCH_SEND_TO_ETH:
		var claim *types.MsgBatchSendToEthClaim = (ethClaim).(*types.MsgBatchSendToEthClaim)

		// Get the batch associated with the claim
		contract, err := types.NewEthAddress(claim.TokenContract)
		if err != nil {
			return nil, fmt.Errorf("attestation contains claim with invalid contract (%v): %v", claim.TokenContract, err)
		}
		outgoingBatch := k.GetOutgoingTxBatch(ctx, claim.EvmChainPrefix, *contract, claim.BatchNonce)
		if outgoingBatch == nil {
			return nil, fmt.Errorf("unable to find batch for attestation with event nonce %v: %v", claim.EventNonce, err)
		}

		// Finally, calculate the total value the batch represents
		change = sdk.NewCoins(outgoingBatch.TotalValue(claim.EvmChainPrefix))

	// ERC20 Deployed
	case types.CLAIM_TYPE_ERC20_DEPLOYED:
		// An ERC20 deploy indicates that the token originates on cosmos, and thus cannot affect its bank supply
		change = sdk.Coins{}

	// Valset Updated
	case types.CLAIM_TYPE_VALSET_UPDATED:
		var claim *types.MsgValsetUpdatedClaim = (ethClaim).(*types.MsgValsetUpdatedClaim)
		if claim.RewardAmount.GT(sdk.ZeroInt()) && claim.RewardToken != types.ZeroAddressString {
			rewardAddress, err := types.NewEthAddress(claim.RewardToken)
			if err != nil {
				return nil, sdkerrors.Wrap(err, "invalid reward token on claim")
			}
			// Check if coin is Cosmos-originated asset and get denom
			isCosmosOriginated, denom := k.ERC20ToDenomLookup(ctx, claim.EvmChainPrefix, *rewardAddress)
			if !isCosmosOriginated {
				err := sdkerrors.Wrapf(err, "valset updated claim contains invalid reward token (%v)", rewardAddress)
				return nil, err
			}

			change = sdk.NewCoins(sdk.NewCoin(denom, claim.RewardAmount))
		} else {
			change = sdk.Coins{}
		}
	// Error case
	case types.CLAIM_TYPE_UNSPECIFIED:
		return nil, sdkerrors.Wrap(types.ErrInvalidClaim, "claim type unspecified")

	// Logic Call Executed
	case types.CLAIM_TYPE_LOGIC_CALL_EXECUTED:
		var claim *types.MsgLogicCallExecutedClaim = (ethClaim).(*types.MsgLogicCallExecutedClaim)
		logicCall := k.GetOutgoingLogicCall(ctx, claim.EvmChainPrefix, claim.InvalidationId, claim.InvalidationNonce)
		if logicCall == nil {
			return nil, fmt.Errorf("could not find logic call for claim (%v)", ethClaim)
		}

		intLC, err := logicCall.ToInternal()
		if err != nil {
			return nil, fmt.Errorf("invalid logic call (%v): %v", logicCall, err)
		}

		change = intLC.TotalValue(claim.EvmChainPrefix)
	}

	return &change, nil
}
