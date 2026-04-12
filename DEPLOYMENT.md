# Secure-Order: Comprehensive Deployment Guide

This guide covers the full deployment of the Secure-Order system, including the C++ privacy layer, the Go sequencer cluster, and the Solidity smart contracts.

## 📋 System Prerequisites

Ensure you have the following installed:
- **Go**: 1.21 or higher
- **Node.js**: 16.x or higher (with npm)
- **C++**: GCC 9+ or Clang 10+
- **CMake**: 3.10+
- **libsodium**: latest stable (`brew install libsodium` or `apt install libsodium-dev`)
- **Hardhat**: (Included in `package.json`)

---

## 🛠️ Phase 1: Building the Core System

### 1. Build the C++ Privacy Layer
The C++ layer handles the heavy lifting for batch decryption.
```bash
cd cpp
mkdir -p build && cd build
cmake ..
make -j$(nproc)
cd ../..
```
This produces `cpp/build/lib/libprivacy.a`.

### 2. Build Go Binaries
The Go components handle the gRPC server and Raft consensus.
```bash
# Set environment for CGO (to link C++ library)
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"

# Run the build script
./scripts/build-local.sh
```
This will generate binaries in the `bin/` directory: `sequencer`, `client`, `rpc-loadtest`, and `demo`.

---

## 🔗 Phase 2: Smart Contract Deployment

Secure-Order uses an on-chain `OrderVerifier` to record cryptographic commitments of sequenced batches.

### 1. Start a Local EVM Node
We use Hardhat's built-in node for local development.
```bash
./scripts/run-local-evm.sh
```
This starts an EVM at `http://127.0.0.1:8545`.

### 2. Deploy the OrderVerifier Contract
```bash
./scripts/deploy-local-order-verifier.sh
```
This script:
1. Compiles the Solidity contracts.
2. Deploys `OrderVerifier.sol` to the local EVM.
3. Saves the contract address to `.local/evm/order_verifier.address`.

---

## 🚀 Phase 3: Deploying the Sequencer Cluster

### 1. Start a 5-Node Raft Cluster
To demonstrate consensus, we run 5 independent sequencer nodes.
```bash
./scripts/run-5-node-raft.sh --fresh
```
- **Ports**: `12345` (node-1) to `12349` (node-5).
- **Raft State**: Stored in `.local/raft-5/`.

### 2. Start Cluster with EVM Integration
If you want the sequencer to automatically publish commitments to the smart contract:
```bash
./scripts/run-local-raft-with-evm.sh --fresh
```
This script exports the required environment variables:
- `ORDER_VERIFIER_RPC_URL`
- `ORDER_VERIFIER_CONTRACT`
- `ORDER_VERIFIER_PRIVATE_KEY` (using Hardhat's default account #0)
- `ORDER_VERIFIER_CHAIN_ID`

---

## 🔍 Phase 4: Verification and Testing

### 1. Find the Cluster Leader
In Raft, only the leader can accept transactions.
```bash
./scripts/find-leader.sh
```

### 2. Submit Transactions
Using the load test tool to send 500 transactions across 50 concurrent clients:
```bash
# Assume node-1 (port 12345) is leader
ADDR=localhost:12345 ./scripts/load-local-raft.sh
```

### 3. Verify on Smart Contract
You can check the Hardhat logs (`.local/evm/logs/hardhat-node.log`) to see the `commitOrder` transactions being processed by the EVM.

---

## 🛑 Phase 5: Cleanup

To stop all running components (Sequencers and EVM):
```bash
./scripts/stop-5-node-raft.sh
./scripts/stop-local-evm.sh
```

## 📂 Configuration Reference

| Environment Variable | Description | Default (Local) |
|----------------------|-------------|-----------------|
| `ORDER_VERIFIER_RPC_URL` | EVM JSON-RPC URL | `http://127.0.0.1:8545` |
| `ORDER_VERIFIER_CONTRACT` | Address of OrderVerifier | (from address file) |
| `ORDER_VERIFIER_PRIVATE_KEY` | Deployer Private Key | Hardhat Account #0 |
| `ORDER_VERIFIER_CHAIN_ID` | EVM Chain ID | `31337` |
| `LD_LIBRARY_PATH` | Path to C++ libraries | `./cpp/build/lib` |

---

## ☁️ Phase 6: Multi-Node AWS Deployment (Production-like)

To deploy a 5-node Raft cluster across different AWS EC2 instances, follow these detailed steps.

### 1. Infrastructure Setup (VPC)
Before launching instances, create a network environment:
1.  **Open VPC Console**: Search for "VPC" in the AWS Management Console.
2.  **Run Wizard**: Click **Create VPC** and select **"VPC and more"**.
    - **Name**: `secure-order-vpc`
    - **AZs**: `1`
    - **Public Subnets**: `1`
    - **Private Subnets**: `0`
    - Click **Create VPC**.
3.  **Enable Public IPs**: Go to **Subnets**, select your public subnet, click **Actions > Edit subnet settings**, and check **Enable auto-assign public IPv4 address**.

### 2. Configure Security Groups
Create a Security Group named `secure-order-sg` in your new VPC:
- **Inbound Rules**:
  - **SSH (22)**: From your IP.
  - **Custom TCP (7000)**: From `secure-order-sg` (This allows nodes to talk to each other).
  - **Custom TCP (12345)**: From `0.0.0.0/0` (Allows clients to connect).

### 3. Provision EC2 Instances
- **Count**: 5 Instances (e.g., `t3.medium`).
- **Network**: Select `secure-order-vpc` and the public subnet.
- **Security Group**: Select `secure-order-sg`.
- **OS**: Ubuntu 22.04 LTS.

### 4. Install Dependencies on All Nodes
Run this on every instance:
```bash
sudo apt update && sudo apt install -y build-essential cmake libsodium-dev pkg-config golang-go
```

### 4. Build and Prepare Binaries
1. Clone the repo and build the C++ layer on **every node** (see Phase 1).
2. Build the Go binaries using `./scripts/build-local.sh`.

### 5. Synchronize Sequencer Keys (Critical)
The cluster **must** share the same keypair.
1. On **Node 1**, run `./bin/sequencer` once to generate `keys/`.
2. Stop the process.
3. Copy the `keys/` directory from Node 1 to all other nodes using SCP:
   ```bash
   scp -r ./keys/ ubuntu@<NODE_IP>:/home/ubuntu/secureorder/
   ```

### 6. Initialize the Cluster
Note the Private IPs of your 5 nodes. Let's assume:
`10.0.0.1 (N1), 10.0.0.2 (N2), 10.0.0.3 (N3), 10.0.0.4 (N4), 10.0.0.5 (N5)`

**Define the Peer List (Same on all nodes):**
```bash
export PEERS="node-1=10.0.0.1:7000,node-2=10.0.0.2:7000,node-3=10.0.0.3:7000,node-4=10.0.0.4:7000,node-5=10.0.0.5:7000"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"
```

**Run Node 1 (Bootstrap):**
```bash
./bin/sequencer -ordering=raft -raft-node-id=node-1 -raft-bind=10.0.0.1:7000 -raft-peers=$PEERS -raft-bootstrap=true
```

**Run Nodes 2-5 (Joiners):**
*Repeat on each node, changing the ID and bind IP.*
```bash
# Example for Node 2
./bin/sequencer -ordering=raft -raft-node-id=node-2 -raft-bind=10.0.0.2:7000 -raft-peers=$PEERS
```

### 7. Client Connection from External Machine
Clients should target the **Elastic IP** (Public IP) of the current leader on port `12345`.
```bash
./bin/client -addr=<LEADER_PUBLIC_IP>:12345
```

