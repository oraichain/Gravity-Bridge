import "@nomicfoundation/hardhat-toolbox";
import { HardhatUserConfig, task } from "hardhat/config";

task("accounts", "Prints the list of accounts", async (args, hre) => {
  const accounts = await hre.ethers.getSigners();

  for (const account of accounts) {
    console.log(account.address);
  }
});

const config: HardhatUserConfig = {
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
      accounts: [{ privateKey: "0xbbfb76c92cd13796899f63dc6ead6d2420e8d0bc502d42bd5773c2d4b8897f08", balance: "10000000000000000000000" }],
      forking: {
        url: "https://rpc.ankr.com/eth_goerli",
        blockNumber: 8218229
      },
      mining: {
        auto: false,
        interval: 2000
      }
    }
  },
  typechain: {
    outDir: "typechain",
    target: "ethers-v6",
  },
  gasReporter: {
    enabled: true
  },
  mocha: {
    timeout: 2000000
  }
};

export default config;
