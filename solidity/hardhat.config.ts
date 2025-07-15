import "@nomicfoundation/hardhat-toolbox";
import "hardhat-contract-sizer";
import { task, extendEnvironment } from "hardhat/config";
import {
  HardhatNetworkAccountsUserConfig,
  HardhatUserConfig,
} from "hardhat/types";
import * as dotenv from "dotenv";

dotenv.config();

const PRIVATE_KEY = process.env.PRIVATE_KEY || "0x0000000000000000000000000000000000000000000000000000000000000000";

task("accounts", "Prints the list of accounts", async (args, hre) => {
  const accounts = await hre.ethers.getSigners();

  for (const account of accounts) {
    console.log(await account.getAddress());
  }
});

let accounts: HardhatNetworkAccountsUserConfig | undefined = undefined;
if (process.env.MNEMONIC) {
  accounts = {
    mnemonic: process.env.MNEMONIC,
    path: "m/44'/60'/0'/0",
    initialIndex: 0,
    count: 20,
    passphrase: "",
  };
} else if (process.env.PRIVATE_KEY) {
  accounts = process.env.PRIVATE_KEY.split(/\s*,\s*/).map((pv) => ({
    privateKey: pv,
    balance: "10000000000000000000000",
  }));
}

// You have to export an object to set up your config
// This object can have the following optional entries:
// defaultNetwork, networks, solc, and paths.
// Go to https://buidler.dev/config/ to learn more
const config: HardhatUserConfig = {
  defaultNetwork: "hardhat",
  // This is a sample solc configuration that specifies which version of solc to use
  solidity: {
    compilers: [
      {
        version: "0.8.10",
        settings: {
          optimizer: {
            enabled: true,
          },
        },
      },
      {
        version: "0.8.12",
        settings: {
          optimizer: {
            enabled: true,
          },
        },
      },
      {
        version: "0.8.20",
        settings: {
          optimizer: {
            enabled: true,
          },
        },
      },
    ],
  },
  networks: {
    hardhat: {
      chainId: 420,
      accounts,
      forking: {
        url: "https://rpc.ankr.com/eth_goerli",
        blockNumber: 8218229,
      },
      mining: {
        auto: false,
        interval: 2000,
      },
    },
    bsc: {
      url: "https://1rpc.io/bnb",
      chainId: 56,
      accounts: [PRIVATE_KEY],
      timeout: 120000, // 2 minutes timeout
      httpHeaders: {
        "User-Agent": "hardhat",
        "Content-Type": "application/json"
      },
      gas: "auto",
      gasPrice: "auto",
      gasMultiplier: 1.2,
      allowUnlimitedContractSize: true,
      blockGasLimit: 30000000,
    },
  },
  typechain: {
    outDir: "typechain",
    target: "ethers-v6",
  },
  gasReporter: {
    enabled: true,
  },
  mocha: {
    timeout: 2000000,
  },
};

export default config;
