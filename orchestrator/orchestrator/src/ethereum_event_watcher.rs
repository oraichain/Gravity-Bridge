//! Ethereum Event watcher watches for events such as a deposit to the Gravity Ethereum contract or a validator set update
//! or a transaction batch update. It then responds to these events by performing actions on the Cosmos chain if required

use clarity::{utils::bytes_to_hex_str, Address as EthAddress, Uint256};
use cosmos_gravity::send::send_erc721_claims;
use cosmos_gravity::{
    query::get_last_erc721_event_nonce_for_validator, query::get_last_event_nonce_for_validator,
    send::send_ethereum_claims,
};
use deep_space::Contact;
use deep_space::{
    coin::Coin,
    private_key::{CosmosPrivateKey, PrivateKey},
};
use gravity_proto::gravity::query_client::QueryClient as GravityQueryClient;
use gravity_utils::get_with_retry::get_net_version_with_retry;
use gravity_utils::get_with_retry::{get_block_number_with_retry, get_finalized_block_with_retry};
use gravity_utils::types::{event_signatures::*, SendERC721ToCosmosEvent};
use gravity_utils::{
    error::GravityError,
    types::{
        Erc20DeployedEvent, EthereumEvent, LogicCallExecutedEvent, SendToCosmosEvent,
        TransactionBatchExecutedEvent, ValsetUpdatedEvent,
    },
};
use metrics_exporter::metrics_errors_counter;
use tonic::transport::Channel;
use web30::client::Web3;
use web30::jsonrpc::error::Web3Error;

use crate::oracle_resync::{ContractType, BLOCKS_TO_SEARCH};

pub struct CheckedNonces {
    pub block_number: Uint256,
    pub event_nonce: Uint256,
    pub contract_type: ContractType,
}

#[allow(clippy::too_many_arguments)]
pub async fn check_for_events(
    web3: &Web3,
    contact: &Contact,
    grpc_client: &mut GravityQueryClient<Channel>,
    gravity_contract_address: EthAddress,
    contract_type: ContractType,
    our_private_key: CosmosPrivateKey,
    fee: Coin,
    starting_block: Uint256,
) -> Result<CheckedNonces, GravityError> {
    let our_cosmos_address = our_private_key.to_address(&contact.get_prefix()).unwrap();
    let latest_block = get_latest_safe_block(web3).await;
    trace!(
        "Checking for events starting {} safe {}",
        starting_block,
        latest_block
    );

    // if the latest block is more than BLOCKS_TO_SEARCH ahead do not search the full history
    // comparison only to prevent panic on underflow.
    let latest_block = if latest_block > starting_block
        && latest_block.clone() - starting_block.clone() > BLOCKS_TO_SEARCH.into()
    {
        starting_block.clone() + BLOCKS_TO_SEARCH.into()
    } else {
        latest_block
    };

    if contract_type == ContractType::GravityERC721 {
        let erc721_deposits = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![SENT_ERC721_TO_COSMOS_EVENT_SIG],
            )
            .await;
        trace!("ERC721 deposits {:?}", erc721_deposits);

        if let (Ok(erc721_deposits),) = (erc721_deposits,) {
            let erc721_deposits = SendERC721ToCosmosEvent::from_logs(&erc721_deposits)?;
            trace!("parsed erc721 deposits {:?}", erc721_deposits);

            // note that starting block overlaps with our last checked block, because we have to deal with
            // the possibility that the relayer was killed after relaying only one of multiple events in a single
            // block, so we also need this routine so make sure we don't send in the first event in this hypothetical
            // multi event block again. In theory we only send all events for every block and that will pass of fail
            // atomicly but lets not take that risk.
            let last_event_nonce = get_last_erc721_event_nonce_for_validator(
                grpc_client,
                our_cosmos_address,
                contact.get_prefix(),
            )
            .await?;
            let erc721_deposits =
                SendERC721ToCosmosEvent::filter_by_event_nonce(last_event_nonce, &erc721_deposits);
            if !erc721_deposits.is_empty() {
                info!(
                "Oracle observed erc721 deposit with sender {}, destination {:?}, token id {}, token uri {}, and event nonce {}",
                erc721_deposits[0].sender, erc721_deposits[0].validated_destination, erc721_deposits[0].token_id,erc721_deposits[0].token_uri, erc721_deposits[0].event_nonce
            );
                let res =
                    send_erc721_claims(contact, our_private_key, erc721_deposits.clone(), fee)
                        .await;
                if res.is_err() {
                    error!("Failed to process GravityERC721 claims");
                    metrics_errors_counter(2, "Failed process erc721 claims");
                    return Err(GravityError::CosmosGrpcError(res.unwrap_err()));
                }

                let new_event_nonce = get_last_erc721_event_nonce_for_validator(
                    grpc_client,
                    our_cosmos_address,
                    contact.get_prefix(),
                )
                .await?;

                info!("Current gravityerc721 event nonce is {}", new_event_nonce);

                // since we can't actually trust that the above txresponse is correct we have to check here
                // we may be able to trust the tx response post grpc
                if new_event_nonce == last_event_nonce {
                    return Err(GravityError::InvalidBridgeStateError(
                format!("GravityERC721 claims did not process, trying to update but still on {}, trying again in a moment, check txhash {:?} for errors", last_event_nonce, res),
            ));
                } else {
                    info!(
                        "GravityERC721 laims processed, new nonce {}",
                        new_event_nonce
                    );
                }

                // find the eth block for our newest event nonce
                let erc721_deposits =
                    SendERC721ToCosmosEvent::get_block_for_nonce(new_event_nonce, &erc721_deposits);

                Ok(CheckedNonces {
                    block_number: erc721_deposits.unwrap(),
                    event_nonce: new_event_nonce.into(),
                    contract_type: contract_type,
                })
            } else {
                // no changes
                Ok(CheckedNonces {
                    block_number: latest_block,
                    event_nonce: last_event_nonce.into(),
                    contract_type: contract_type,
                })
            }
        } else {
            error!("Failed to get events");
            metrics_errors_counter(1, "Failed to get events");
            Err(GravityError::EthereumRestError(Web3Error::BadResponse(
                "Failed to get logs!".to_string(),
            )))
        }
    } else {
        let erc20_deposits = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![SENT_TO_COSMOS_EVENT_SIG],
            )
            .await;
        trace!("ERC20 deposits {:?}", erc20_deposits);

        let batches = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![TRANSACTION_BATCH_EXECUTED_EVENT_SIG],
            )
            .await;
        trace!("Batches {:?}", batches);

        let valsets = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![VALSET_UPDATED_EVENT_SIG],
            )
            .await;
        trace!("Valsets {:?}", valsets);

        let erc20_deployed = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![ERC20_DEPLOYED_EVENT_SIG],
            )
            .await;
        trace!("ERC20 Deployments {:?}", erc20_deployed);

        let logic_call_executed = web3
            .check_for_events(
                starting_block.clone(),
                Some(latest_block.clone()),
                vec![gravity_contract_address],
                vec![LOGIC_CALL_EVENT_SIG],
            )
            .await;
        trace!("Logic call executions {:?}", logic_call_executed);

        if let (Ok(valsets), Ok(batches), Ok(erc20_deposits), Ok(deploys), Ok(logic_calls)) = (
            valsets,
            batches,
            erc20_deposits,
            erc20_deployed,
            logic_call_executed,
        ) {
            let valsets = ValsetUpdatedEvent::from_logs(&valsets)?;
            trace!("parsed valsets {:?}", valsets);
            let withdraws = TransactionBatchExecutedEvent::from_logs(&batches)?;
            trace!("parsed batches {:?}", batches);
            let erc20_deposits = SendToCosmosEvent::from_logs(&erc20_deposits)?;
            trace!("parsed erc20 deposits {:?}", erc20_deposits);
            let erc20_deploys = Erc20DeployedEvent::from_logs(&deploys)?;
            trace!("parsed erc20 deploys {:?}", erc20_deploys);
            let logic_calls = LogicCallExecutedEvent::from_logs(&logic_calls)?;
            trace!("logic call executions {:?}", logic_calls);

            // note that starting block overlaps with our last checked block, because we have to deal with
            // the possibility that the relayer was killed after relaying only one of multiple events in a single
            // block, so we also need this routine so make sure we don't send in the first event in this hypothetical
            // multi event block again. In theory we only send all events for every block and that will pass of fail
            // atomicly but lets not take that risk.
            let last_event_nonce = get_last_event_nonce_for_validator(
                grpc_client,
                our_cosmos_address,
                contact.get_prefix(),
            )
            .await?;
            let valsets = ValsetUpdatedEvent::filter_by_event_nonce(last_event_nonce, &valsets);
            let erc20_deposits =
                SendToCosmosEvent::filter_by_event_nonce(last_event_nonce, &erc20_deposits);
            let withdraws =
                TransactionBatchExecutedEvent::filter_by_event_nonce(last_event_nonce, &withdraws);
            let erc20_deploys =
                Erc20DeployedEvent::filter_by_event_nonce(last_event_nonce, &erc20_deploys);
            let logic_calls =
                LogicCallExecutedEvent::filter_by_event_nonce(last_event_nonce, &logic_calls);

            if !valsets.is_empty() {
                info!(
                    "Oracle observed Valset update with nonce {} and event nonce {}",
                    valsets[0].valset_nonce, valsets[0].event_nonce
                )
            }
            if !erc20_deposits.is_empty() {
                info!(
                "Oracle observed erc20 deposit with sender {}, destination {:?}, amount {}, and event nonce {}",
                erc20_deposits[0].sender, erc20_deposits[0].validated_destination, erc20_deposits[0].amount, erc20_deposits[0].event_nonce
            )
            }
            if !withdraws.is_empty() {
                info!(
                    "Oracle observed batch with nonce {}, contract {}, and event nonce {}",
                    withdraws[0].batch_nonce, withdraws[0].erc20, withdraws[0].event_nonce
                )
            }
            if !erc20_deploys.is_empty() {
                let v = erc20_deploys[0].clone();
                if v.cosmos_denom.len() < 1000 && v.name.len() < 1000 && v.symbol.len() < 1000 {
                    info!(
                "Oracle observed ERC20 deployment with denom {} erc20 name {} and symbol {} and event nonce {}",
                erc20_deploys[0].cosmos_denom, erc20_deploys[0].name, erc20_deploys[0].symbol, erc20_deploys[0].event_nonce,
                );
                } else {
                    info!(
                        "Oracle observed ERC20 deployment with  event nonce {}",
                        erc20_deploys[0].event_nonce,
                    );
                }
            }
            if !logic_calls.is_empty() {
                info!(
                    "Oracle observed logic call execution with ID {} Nonce {} and event nonce {}",
                    bytes_to_hex_str(&logic_calls[0].invalidation_id),
                    logic_calls[0].invalidation_nonce,
                    logic_calls[0].event_nonce
                )
            }
            if !erc20_deposits.is_empty()
                || !withdraws.is_empty()
                || !erc20_deploys.is_empty()
                || !logic_calls.is_empty()
                || !valsets.is_empty()
            {
                // concat vectors into one big vector

                let res = send_ethereum_claims(
                    contact,
                    our_private_key,
                    erc20_deposits.clone(),
                    withdraws.clone(),
                    erc20_deploys.clone(),
                    logic_calls.clone(),
                    valsets.clone(),
                    fee,
                )
                .await?;
                let new_event_nonce = get_last_event_nonce_for_validator(
                    grpc_client,
                    our_cosmos_address,
                    contact.get_prefix(),
                )
                .await?;

                info!("Current gravity event nonce is {}", new_event_nonce);

                // since we can't actually trust that the above txresponse is correct we have to check here
                // we may be able to trust the tx response post grpc
                if new_event_nonce == last_event_nonce {
                    return Err(GravityError::InvalidBridgeStateError(
                    format!("Gravity claims did not process, trying to update but still on {}, trying again in a moment, check txhash {:?} for errors", last_event_nonce, res),
                ));
                } else {
                    info!("Gravity claims processed, new nonce {}", new_event_nonce);
                }

                // find the eth block for our newest event nonce
                let valsets = ValsetUpdatedEvent::get_block_for_nonce(new_event_nonce, &valsets);
                let erc20_deposits =
                    SendToCosmosEvent::get_block_for_nonce(new_event_nonce, &erc20_deposits);
                let withdraws =
                    TransactionBatchExecutedEvent::get_block_for_nonce(new_event_nonce, &withdraws);
                let erc20_deploys =
                    Erc20DeployedEvent::get_block_for_nonce(new_event_nonce, &erc20_deploys);
                let logic_calls =
                    LogicCallExecutedEvent::get_block_for_nonce(new_event_nonce, &logic_calls);

                let block = match (
                    valsets,
                    erc20_deposits,
                    withdraws,
                    erc20_deploys,
                    logic_calls,
                ) {
                    (Some(b), _, _, _, _) => b,
                    (_, Some(b), _, _, _) => b,
                    (_, _, Some(b), _, _) => b,
                    (_, _, _, Some(b), _) => b,
                    (_, _, _, _, Some(b)) => b,
                    _ => panic!("It's impossible for an event to be in more than one list!"),
                };

                Ok(CheckedNonces {
                    block_number: block,
                    event_nonce: new_event_nonce.into(),
                    contract_type: contract_type,
                })
            } else {
                // no changes
                Ok(CheckedNonces {
                    block_number: latest_block,
                    event_nonce: last_event_nonce.into(),
                    contract_type: contract_type,
                })
            }
        } else {
            error!("Failed to get events");
            metrics_errors_counter(1, "Failed to get events");
            Err(GravityError::EthereumRestError(Web3Error::BadResponse(
                "Failed to get logs!".to_string(),
            )))
        }
    }
}

/// The latest 'safe block' for Ethereum event checking. This is used to prevent the bridge from
/// accepting deposits that are not finalized and may be subject to a re-org, resulting in the attacker
/// recieving tokens that are not actually in the bridge contract.
///
/// Ethereum POS does have finality but is still subject to chain forks and re-orgs in complex
/// ways. Finality can be delayed many hundreds of blocks and hours of wall time in the worst case
/// scenario. This function simply asks the full node what the latest finalized block is.
///
/// This function makes an attempt at being safe across all chain-ids using 96 blocks as a conservative
/// finality value in the case that we are unable to determine the consensus method of the chain.
///
/// As a quick summary of 'why 96?' we summarize epoch and slot timing of Ethereum proof of
/// stake consensus, each block is a slot, and each epoch is 32 slots. You are not garunteed
/// to have a block produced every slot though and an epoch is no garuntee of finalization.
/// epochs are not instantly final and become final only once the following epoch is 'justified'
/// during normal protocol operation 3 epochs will always result in finalization.
///
/// In the case that the unknown chain is a proof of work chain 96 blocks is extremely deep for a
/// re-org but saftey will always be determined by mining power.
///
/// https://ethereum.org/en/developers/docs/consensus-mechanisms/pos/
/// https://arxiv.org/pdf/2003.03052.pdf
/// https://eth2book.info/altair/part2/incentives/inactivity
/// https://hackmd.io/@prysmaticlabs/finality
///
pub async fn get_latest_safe_block(web3: &Web3) -> Uint256 {
    let net_version = get_net_version_with_retry(web3).await;
    let block_number = get_block_number_with_retry(web3).await;

    match net_version {
        // Mainline Ethereum, Ethereum classic, or the Ropsten, Kotti, Mordor testnets
        // all Ethereum proof of stake Chains
        1 | 3 | 6 | 7 => get_finalized_block_with_retry(web3).await,
        // Dev, our own Gravity Ethereum testnet, and Hardhat respectively
        // all single signer chains with no chance of any reorgs
        2018 | 15 | 31337 => block_number,
        // Rinkeby and Goerli use Clique (POA) Consensus, finality takes
        // up to num validators blocks. Number is higher than Ethereum based
        // on experience with operational issues
        // 11155111 is used by Sepolia
        4 | 5 | 11155111 => block_number - 10u8.into(),
        // assume the safe option where we don't know
        _ => block_number - 96u8.into(),
    }
}
