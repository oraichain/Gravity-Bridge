import { ethers } from "hardhat";
import {Gravity__factory} from "../typechain/factories/contracts/Gravity__factory";

async function main() {
    const signers = await ethers.getSigners();
    console.log("signers", signers[0].address);

    const usdtContract = await ethers.getContractAt("IERC20Upgradeable", "0x55d398326f99059ff775485246999027b3197955", signers[0]);
    const currentAllowance = await usdtContract.allowance(signers[0].address, "0x9a0A02B296240D2620E339cCDE386Ff612f07Be5");
    console.log("currentAllowance", currentAllowance);

    const result = await usdtContract.approve("0x9a0A02B296240D2620E339cCDE386Ff612f07Be5", "100000000000000000", { from: signers[0].address });
    await result.wait();

    const currentAllowance2 = await usdtContract.allowance(signers[0].address, "0x9a0A02B296240D2620E339cCDE386Ff612f07Be5");
    console.log("currentAllowance2", currentAllowance2);


    const gravity = await Gravity__factory.connect(
        "0x9a0A02B296240D2620E339cCDE386Ff612f07Be5",
        signers[0]
    );

    const balance = await signers[0].provider?.getBalance(signers[0].address);
    console.log("Account balance:", ethers.formatEther(balance || 0), "BNB");

    gravity.sendToCosmos(
        "0x55d398326f99059ff775485246999027b3197955",
        "channel-8/viex106hhf6s5tfdgzajwct89ldqv6pklm5v037h65w",
        "100000000000000000",
        {
            from: signers[0].address,
        }
    );
}

// We recommend this pattern to be able to use async/await everywhere
// and properly handle errors.
main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
