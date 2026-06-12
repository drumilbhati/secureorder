# Secure-Order: Comprehensive Deployment Guide

This guide covers the full deployment of Secure-Order, including:

- the C++ privacy layer
- the Go sequencer cluster
- the Hardhat-based local EVM
- the `OrderVerifier` smart contract
- single-host local demos
- multi-node AWS deployment

It also documents the operational fixes validated in the current deployment workflow:

- build the C++ library before any Go binaries
- build the sequencer with EVM support enabled
- start Hardhat manually for this repository's current Hardhat version
- deploy the contract to `localhost`, not the simulated in-process network
- use the same sequencer keypair on every cluster node
- bootstrap Raft only once for a fresh cluster
- use `nohup` or a service manager so the sequencer survives SSH logout

---

## 📋 System Prerequisites

Install the following on any machine that will build or run Secure-Order:

- **Go**: 1.21 or higher
- **Node.js**: 18+ recommended
- **npm**
- **C++ toolchain**: GCC 9+ or Clang 10+
- **CMake**: 3.10+
- **pkg-config**
- **libsodium**: latest stable (`brew install libsodium` or `apt install libsodium-dev`)

### Ubuntu packages

```bash
sudo apt update && sudo apt install -y build-essential cmake libsodium-dev pkg-config golang-go nodejs npm
```

### macOS packages

```bash
brew install libsodium cmake pkg-config go node
```

---

## 🛠️ Phase 1: Building the Core System

## 1. Build the C++ Privacy Layer

The Go binaries link against the static C++ library `libprivacy.a`.  
If this library does not exist, Go builds fail with linker errors like:

- `cannot find -lprivacy`

Build it first:

```bash
cd cpp
mkdir -p build
cd build
cmake -DCMAKE_INSTALL_PREFIX=. ..
make -j"$(nproc)"
make install
cd ../..
```

Expected artifact:

```bash
ls -l cpp/build/lib/libprivacy.a
```

## 2. Build the Go Binaries

Set the linker/runtime environment first:

```bash
cd ~/secureorder
export CXXFLAGS="-std=c++17"
export LDFLAGS="-L./cpp/build/lib -lprivacy -lsodium -lstdc++"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"
```

### Build without EVM settlement support

This is sufficient for local Raft-only demos:

```bash
./scripts/build-local.sh
```

### Build with EVM settlement support

For any deployment that should publish commitments to `OrderVerifier`, build the sequencer with the `evm` build tag:

```bash
CGO_ENABLED=1 go build -tags evm -o bin/sequencer ./cmd/sequencer
CGO_ENABLED=1 go build -o bin/client ./cmd/client
CGO_ENABLED=1 go build -o bin/rpc-loadtest ./cmd/rpc-loadtest
```

Verify the binaries:

```bash
ls -l bin/sequencer bin/client bin/rpc-loadtest
```

---

## 🔗 Phase 2: Smart Contract Deployment

Secure-Order publishes a cryptographic commitment for each processed batch to the on-chain `OrderVerifier` contract.

## Important note about the current repository scripts

For the current dependency versions in this repository:

- `./scripts/run-local-evm.sh` is outdated because it starts Hardhat with `--auto-mine`, which is not accepted by the installed Hardhat version
- `./scripts/deploy-local-order-verifier.sh` deploys to `--network hardhatMainnet`, which is not the same as the long-running RPC node on `127.0.0.1:8545`

For reliable operation, use the **manual Hardhat start and deployment procedure below**.

## 1. Start a Local EVM Node

From the repository root:

```bash
mkdir -p .local/evm/logs .local/evm/pids
nohup npx hardhat node --hostname 0.0.0.0 > .local/evm/logs/hardhat-node.log 2>&1 < /dev/null &
echo $! > .local/evm/pids/hardhat.pid
```

Verify it is running:

```bash
cat .local/evm/pids/hardhat.pid
ps -fp "$(cat .local/evm/pids/hardhat.pid)"
ss -ltnp | grep 8545
```

Expected RPC endpoint:

```text
http://127.0.0.1:8545
```

If you want other machines in the same VPC to use this EVM, they should connect to the host's **private IP** on port `8545`.

## 2. Deploy the `OrderVerifier` Contract to the Running Node

Deploy to the **running localhost node**:

```bash
deploy_output="$(npx hardhat run scripts/deploy-order-verifier.ts --network localhost)"
echo "$deploy_output"
address="$(echo "$deploy_output" | awk '/OrderVerifier deployed at:/ {print $4}')"
test -n "$address"
mkdir -p .local/evm
printf '%s\n' "$address" > .local/evm/order_verifier.address
cat .local/evm/order_verifier.address
```

This saves the deployed address to:

```text
.local/evm/order_verifier.address
```

## 3. Hardhat Default Account #0

For the local Hardhat node, the default funded private key used in this deployment flow is:

```text
ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
```

This is acceptable only for local development/testing.

---

## 🚀 Phase 3: Local Cluster Deployment

## 1. Start a 5-Node Local Raft Cluster

For local Raft-only testing:

```bash
./scripts/run-5-node-raft.sh --fresh
```

This starts 5 nodes locally with separate gRPC and Raft ports.

## 2. Local Cluster with EVM Integration

Because the bundled EVM helper scripts are outdated, use this manual flow instead:

### Start Hardhat

```bash
mkdir -p .local/evm/logs .local/evm/pids
nohup npx hardhat node --hostname 0.0.0.0 > .local/evm/logs/hardhat-node.log 2>&1 < /dev/null &
echo $! > .local/evm/pids/hardhat.pid
```

### Deploy the contract

```bash
deploy_output="$(npx hardhat run scripts/deploy-order-verifier.ts --network localhost)"
echo "$deploy_output"
address="$(echo "$deploy_output" | awk '/OrderVerifier deployed at:/ {print $4}')"
printf '%s\n' "$address" > .local/evm/order_verifier.address
```

### Export settlement environment

```bash
export ORDER_VERIFIER_CONTRACT="$(cat .local/evm/order_verifier.address)"
export ORDER_VERIFIER_RPC_URL="http://127.0.0.1:8545"
export ORDER_VERIFIER_CHAIN_ID="31337"
export ORDER_VERIFIER_PRIVATE_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"
```

### Then start the local cluster

You can either use local scripts for Raft-only behavior, or start nodes manually with the exported env if you want the EVM-enabled binary to publish commitments.

---

## 🔍 Phase 4: Verification and Testing

## 1. Check the Leader

Only the leader publishes commitments to the EVM.

For local script-managed clusters:

```bash
./scripts/find-leader.sh
```

For manual AWS deployment, inspect the logs and identify the node currently acting as leader.

## 2. Submit Transactions

Example local load test:

```bash
./bin/rpc-loadtest -addr=127.0.0.1:12345 -clients=100 -requests=20
```

## 3. Verify Sequencer Processing

Look for logs like:

```text
Processing batch of 1000 transactions...
Batch commitment: <hex>
Successfully decrypted batch of 1000
```

## 4. Verify EVM Settlement

On the **leader** node's `logs/sequencer.log`, successful settlement looks like:

```text
Successfully published batch commitment to EVM: <hex>
```

On the Hardhat node, you should see `commitOrder` calls in:

```text
.local/evm/logs/hardhat-node.log
```

Typical evidence:

- `eth_sendRawTransaction`
- `Contract call: OrderVerifier#commitOrder`
- `To: <contract address>`

---

## 🛑 Phase 5: Cleanup

## Stop Local Sequencers

If started via the local script:

```bash
./scripts/stop-5-node-raft.sh
```

If started manually:

```bash
kill "$(cat sequencer.pid)"
rm -f sequencer.pid
```

## Stop Hardhat

If started manually:

```bash
kill "$(cat .local/evm/pids/hardhat.pid)"
rm -f .local/evm/pids/hardhat.pid
```

Or:

```bash
pkill -f "hardhat node" || true
pkill -f "npm exec hardhat node" || true
```

---

## 📂 Configuration Reference

| Environment Variable | Description | Typical Value |
|----------------------|-------------|---------------|
| `ORDER_VERIFIER_RPC_URL` | EVM JSON-RPC URL | `http://127.0.0.1:8545` or `http://<private-ip>:8545` |
| `ORDER_VERIFIER_CONTRACT` | Address of deployed `OrderVerifier` | from `.local/evm/order_verifier.address` |
| `ORDER_VERIFIER_PRIVATE_KEY` | Private key used to publish `commitOrder` | Hardhat Account #0 for local development |
| `ORDER_VERIFIER_CHAIN_ID` | Chain ID of the EVM | `31337` |
| `PEERS` | Raft peer configuration | `node-1=<ip>:7000,...` |
| `LD_LIBRARY_PATH` | Runtime path to C++ library | `./cpp/build/lib` |

---

## ☁️ Phase 6: Multi-Node AWS Deployment (Recommended Procedure)

This section documents the validated AWS deployment procedure for a 5-node Raft cluster with Node 1 also hosting the Hardhat EVM.

## 1. Infrastructure Setup

Create a VPC and subnet layout suitable for 5 public EC2 instances.

Recommended:
- 1 VPC
- 1 public subnet
- auto-assign public IPv4 enabled

## 2. Security Groups

Create a security group, for example `secure-order-sg`, with at least:

- **SSH (22)** from your workstation IP
- **TCP 7000** from `secure-order-sg`
- **TCP 12345** from clients or from `0.0.0.0/0` if needed
- **TCP 8545** from `secure-order-sg` if other nodes must reach Node 1's EVM

## 3. Provision EC2 Instances

Recommended baseline:
- 5 instances
- Ubuntu 24.04 LTS or Ubuntu 22.04 LTS
- same VPC/subnet/security group

Example cluster:

| Node | Role | Private IP |
|------|------|------------|
| Node 1 | Bootstrap + EVM Host | `172.31.40.29` |
| Node 2 | Voter | `172.31.43.7` |
| Node 3 | Voter | `172.31.45.108` |
| Node 4 | Voter | `172.31.47.180` |
| Node 5 | Voter | `172.31.37.39` |

## 4. Install Dependencies on All Nodes

Run on every instance:

```bash
sudo apt update && sudo apt install -y build-essential cmake libsodium-dev pkg-config golang-go nodejs npm
```

## 5. Build the Native Library and Go Binaries on All Nodes

Run on every instance:

```bash
cd ~/secureorder

sudo apt update
sudo apt install -y build-essential cmake libsodium-dev pkg-config

mkdir -p cpp/build
cd cpp/build
cmake -DCMAKE_INSTALL_PREFIX=. ..
make -j"$(nproc)"
make install

cd ~/secureorder
ls -l cpp/build/lib/libprivacy.a

export CXXFLAGS="-std=c++17"
export LDFLAGS="-L./cpp/build/lib -lprivacy -lsodium -lstdc++"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"

CGO_ENABLED=1 go build -tags evm -o bin/sequencer ./cmd/sequencer
CGO_ENABLED=1 go build -o bin/client ./cmd/client
CGO_ENABLED=1 go build -o bin/rpc-loadtest ./cmd/rpc-loadtest
```

## 6. Synchronize Sequencer Keys

All nodes must share the same `keys/` directory.

### Generate keys on Node 1 if needed

Run `./bin/sequencer` once, then stop it.

### Copy the directory to all nodes

```bash
scp -r ./keys/ ubuntu@<NODE_IP>:/home/ubuntu/secureorder/
```

## 7. Start the Hardhat EVM on Node 1

On **Node 1 only**:

```bash
cd ~/secureorder
mkdir -p .local/evm/logs .local/evm/pids
nohup npx hardhat node --hostname 0.0.0.0 > .local/evm/logs/hardhat-node.log 2>&1 < /dev/null &
echo $! > .local/evm/pids/hardhat.pid
```

Verify:

```bash
cat .local/evm/pids/hardhat.pid
ps -fp "$(cat .local/evm/pids/hardhat.pid)"
ss -ltnp | grep 8545
```

## 8. Deploy a Fresh Contract on Node 1

On **Node 1 only**:

```bash
cd ~/secureorder
deploy_output="$(npx hardhat run scripts/deploy-order-verifier.ts --network localhost)"
echo "$deploy_output"
address="$(echo "$deploy_output" | awk '/OrderVerifier deployed at:/ {print $4}')"
test -n "$address"
mkdir -p .local/evm
printf '%s\n' "$address" > .local/evm/order_verifier.address
cat .local/evm/order_verifier.address
```

Example resulting address:

```text
0x5fbdb2315678afecb367f032d93f642f64180aa3
```

## 9. Export the Common Runtime Environment

Use the same values on all 5 nodes, adjusting only node-specific Raft IDs and bind addresses.

```bash
export ORDER_VERIFIER_CONTRACT="0x5fbdb2315678afecb367f032d93f642f64180aa3"
export ORDER_VERIFIER_RPC_URL="http://172.31.40.29:8545"
export ORDER_VERIFIER_CHAIN_ID="31337"
export ORDER_VERIFIER_PRIVATE_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
export PEERS="node-1=172.31.40.29:7000,node-2=172.31.43.7:7000,node-3=172.31.45.108:7000,node-4=172.31.47.180:7000,node-5=172.31.37.39:7000"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"
mkdir -p logs
```

## 10. Fresh Cluster vs Existing Cluster

### Fresh cluster
Use `-raft-bootstrap=true` on **Node 1 only**, exactly once.

### Existing cluster restart
Do **not** use `-raft-bootstrap=true`.

If `.local/raft/data/<node>` already exists and you are simply restarting the cluster, start all nodes without bootstrap.

## 11. Start the Cluster on AWS

### Fresh cluster creation

#### Node 1
```bash
nohup ./bin/sequencer -ordering=raft -publisher-type=evm -grpc-addr=:12345 -raft-node-id=node-1 -raft-bind=172.31.40.29:7000 -raft-peers="$PEERS" -raft-data-dir=.local/raft/data/node-1 -raft-bootstrap=true > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

#### Node 2
```bash
nohup ./bin/sequencer -ordering=raft -publisher-type=evm -grpc-addr=:12345 -raft-node-id=node-2 -raft-bind=172.31.43.7:7000 -raft-peers="$PEERS" -raft-data-dir=.local/raft/data/node-2 > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

#### Node 3
```bash
nohup ./bin/sequencer -ordering=raft -publisher-type=evm -grpc-addr=:12345 -raft-node-id=node-3 -raft-bind=172.31.45.108:7000 -raft-peers="$PEERS" -raft-data-dir=.local/raft/data/node-3 > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

#### Node 4
```bash
nohup ./bin/sequencer -ordering=raft -publisher-type=evm -grpc-addr=:12345 -raft-node-id=node-4 -raft-bind=172.31.47.180:7000 -raft-peers="$PEERS" -raft-data-dir=.local/raft/data/node-4 > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

#### Node 5
```bash
nohup ./bin/sequencer -ordering=raft -publisher-type=evm -grpc-addr=:12345 -raft-node-id=node-5 -raft-bind=172.31.37.39:7000 -raft-peers="$PEERS" -raft-data-dir=.local/raft/data/node-5 > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

### Existing cluster restart

Use the same commands as above, but remove `-raft-bootstrap=true` from Node 1.

## 12. Keep Sequencers Running After SSH Logout

Use:

```bash
nohup ./bin/sequencer ... > logs/sequencer.log 2>&1 < /dev/null &
echo $! > sequencer.pid
```

This detaches the process so it continues running after logout.

## 13. Check Node Health

On each node:

```bash
cat sequencer.pid
ps -fp "$(cat sequencer.pid)"
tail -n 20 logs/sequencer.log
```

## 14. Verify EVM Publishing on AWS

On the leader node, look for:

```text
Successfully published batch commitment to EVM: <hex>
```

On Node 1's Hardhat log, look for:

- `eth_sendRawTransaction`
- `OrderVerifier#commitOrder`
- `To: <deployed contract address>`

Example:

```text
Contract call:       OrderVerifier#commitOrder
To:                  0x5fbdb2315678afecb367f032d93f642f64180aa3
```

## 15. Client Access

External clients should connect to the current leader's public IP on port `12345`:

```bash
./bin/client -addr=<LEADER_PUBLIC_IP>:12345
```

Because follower proxying is implemented, clients may also be able to submit through followers, but targeting the leader is still the clearest operational model.

---

## ☸️ Phase 7: Kubernetes Deployment and Autoscaling

Secure-Order supports native Kubernetes deployment using a `StatefulSet`. This provides stable network identities (`sequencer-0`, `sequencer-1`, etc.) and persistent storage for the Raft log.

### 1. Dynamic Raft Membership
The sequencer implements a `JoinCluster` gRPC endpoint. When the Kubernetes `HorizontalPodAutoscaler` (HPA) spins up a new pod (e.g., `sequencer-3`), the pod:
- Detects it has no local Raft state.
- Contacts `sequencer-0` (the bootstrap seed) via gRPC.
- Requests to join the cluster.
- The leader formally adds the new pod to the Raft consensus group.

### 2. Containerization
Build the multi-stage Docker image which includes the C++ privacy layer and the Go sequencing engine:

```bash
docker build -t secureorder/sequencer:latest .
```

### 3. Deploying to Kubernetes
Apply the provided manifests in the `k8s/` directory:

```bash
# Headless service for stable DNS
kubectl apply -f k8s/service.yaml

# StatefulSet for managed pods and persistence
kubectl apply -f k8s/statefulset.yaml

# HPA for load-based scaling
kubectl apply -f k8s/hpa.yaml
```

### 4. Verifying Autoscaling
To test the autoscaling behavior:

1. **Monitor the HPA**:
   ```bash
   kubectl get hpa sequencer-hpa -w
   ```

2. **Generate Load**:
   Use the `rpc-loadtest` tool to increase CPU utilization:
   ```bash
   kubectl port-forward svc/sequencer 12345:12345
   ./bin/rpc-loadtest -addr localhost:12345 -concurrency 50 -duration 5m
   ```

3. **Observe Scale-Out**:
   Kubernetes will detect the load and increase the replica count. Check the logs of the newly created pods to see them joining the cluster:
   ```bash
   kubectl logs sequencer-3 | grep "Successfully joined cluster"
   ```

---