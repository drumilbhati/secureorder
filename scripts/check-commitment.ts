import { network } from "hardhat";
import { parseAbiItem } from "viem";

async function main() {
  const contractAddress = process.env.ORDER_VERIFIER_CONTRACT;
  const commitment = process.argv[2];

  if (!contractAddress || !commitment) {
    console.error("Usage: ORDER_VERIFIER_CONTRACT=0x... npx hardhat run scripts/check-commitment.ts --network localhost <commitment_hex>");
    process.exit(1);
  }

  const { viem } = await network.connect();
  const publicClient = await viem.getPublicClient();

  const isValid = await publicClient.readContract({
    address: contractAddress as `0x${string}`,
    abi: [
      parseAbiItem("function validCommitments(bytes32) public view returns (bool)")
    ],
    functionName: "validCommitments",
    args: [commitment as `0x${string}`],
  });

  console.log(`Commitment ${commitment} is valid: ${isValid}`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
