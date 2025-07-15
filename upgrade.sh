gravity tx gov submit-proposal software-upgrade monitorevent --title "v1.0.9" --description "upgrade orai bridge to v1.0.9: add event for monitoring orai bridge"  --from testing --keyring-backend test --upgrade-height 29924600 --upgrade-info "https://github.com/oraidex/Gravity-Bridge/releases/tag/v1.0.9" --deposit 10000000uoraib --node https://bridge-v2.rpc.orai.io:443

gravity tx gov vote 24 yes --from 
gravity q gov proposal 24 --node https://bridge-v2.rpc.orai.io:443
