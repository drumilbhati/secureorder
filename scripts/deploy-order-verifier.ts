import { network } from "hardhat";

async function main() {
  const { viem } = await network.connect();

  const orderVerifier = await viem.deployContract("OrderVerifier");
  console.log(`OrderVerifier deployed at: ${orderVerifier.address}`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
