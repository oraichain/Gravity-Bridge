use crate::args::RelayerOpts;
use crate::config::config_exists;
use crate::config::load_keys;
use crate::utils::parse_bridge_ethereum_address_with_exit;
use crate::utils::print_relaying_explanation;
use cosmos_gravity::query::get_gravity_params;
use cosmos_gravity::query::query_evm_chain_from_net_version;
use deep_space::{CosmosPrivateKey, PrivateKey};
use gravity_utils::connection_prep::check_for_fee;
use gravity_utils::connection_prep::{
    check_for_eth, create_rpc_connections, wait_for_cosmos_node_ready,
};
use gravity_utils::get_with_retry::get_net_version_with_retry;
use gravity_utils::types::BatchRequestMode;
use gravity_utils::types::RelayerConfig;
use relayer::main_loop::all_relayer_loops;
use relayer::main_loop::TIMEOUT;
use std::path::Path;
use std::process::exit;

pub async fn relayer(
    args: RelayerOpts,
    address_prefix: String,
    home_dir: &Path,
    config: RelayerConfig,
) {
    let cosmos_grpc = args.cosmos_grpc;
    let ethereum_rpc = args.ethereum_rpc;
    let ethereum_key = args.ethereum_key;
    let cosmos_key = args.cosmos_phrase;

    let connections = create_rpc_connections(
        address_prefix,
        Some(cosmos_grpc),
        Some(ethereum_rpc),
        TIMEOUT,
    )
    .await;

    let ethereum_key = if let Some(k) = ethereum_key {
        k
    } else {
        let mut k = None;
        if config_exists(home_dir) {
            let keys = load_keys(home_dir);
            if let Some(stored_key) = keys.ethereum_key {
                k = Some(stored_key)
            }
        }
        if k.is_none() {
            error!("You must specify an Ethereum key!");
            error!("To generate, register, and store a key use `gbt keys register-orchestrator-address`");
            error!("Store an already registered key using 'gbt keys set-ethereum-key`");
            error!("To run from the command line, with no key storage use 'gbt relayer --ethereum-key your key' ");
            exit(1);
        }
        k.unwrap()
    };
    let cosmos_key = if let Some(k) = cosmos_key {
        Some(k)
    } else if config_exists(home_dir) {
        let keys = load_keys(home_dir);
        keys.orchestrator_phrase
            .map(|stored_key| CosmosPrivateKey::from_phrase(&stored_key, "").unwrap())
    } else {
        None
    };

    let public_eth_key = ethereum_key.to_address();
    info!("Starting Gravity Relayer");
    info!("Ethereum Address: {}", public_eth_key);

    let contact = connections.contact.clone().unwrap();
    let web3 = connections.web3.unwrap();
    let mut grpc = connections.grpc.unwrap();

    let net_version = get_net_version_with_retry(&web3).await;

    // get correct evm_chain from rpc by querying net_id
    let evm_chain_prefix = match query_evm_chain_from_net_version(&mut grpc, net_version).await {
        Some(evm_chain) => evm_chain.evm_chain_prefix,
        None => {
            error!("Could not find the matching net version of evm chains on the network. Network from eth-rpc: {}", net_version);
            return;
        }
    };

    // check if the cosmos node is syncing, if so wait for it
    // we can't move any steps above this because they may fail on an incorrect
    // historic chain state while syncing occurs
    wait_for_cosmos_node_ready(&contact).await;
    check_for_eth(public_eth_key, &web3).await;

    // get the gravity parameters
    let params = get_gravity_params(&mut grpc)
        .await
        .expect("Failed to get Gravity Bridge module parameters!");

    let evm_chain_params = params
        .evm_chain_params
        .iter()
        .find(|p| p.evm_chain_prefix.eq(&evm_chain_prefix))
        .expect("Failed to get evm chain params");

    // get the gravity contract address, if not provided
    let contract_address =
        parse_bridge_ethereum_address_with_exit(&evm_chain_params.bridge_ethereum_address);

    info!("Gravity contract address {}", contract_address);

    // setup and explain relayer settings
    if let (Some(fee), Some(cosmos_key)) = (args.fees.clone(), cosmos_key) {
        if config.batch_request_mode != BatchRequestMode::None {
            let public_cosmos_key = cosmos_key.to_address(&contact.get_prefix()).unwrap();
            check_for_fee(&fee, public_cosmos_key, &contact).await;
            print_relaying_explanation(&config, true)
        } else {
            print_relaying_explanation(&config, false)
        }
    } else {
        print_relaying_explanation(&config, false)
    }

    all_relayer_loops(
        cosmos_key,
        ethereum_key,
        web3,
        contact,
        grpc,
        &evm_chain_prefix,
        contract_address,
        evm_chain_params.gravity_id.clone(),
        args.fees,
        config,
    )
    .await;
}
