# Secure-Order: FIFO Sequencing Layer for MEV Mitigation

Secure-Order is a high-performance, modular sequencing layer designed to eliminate Miner Extractable Value (MEV) exploitation through **Encrypted Transaction Ordering**. By using a commit-reveal scheme, the system ensures that transactions are sequenced in a verifiable FIFO (First-In-First-Out) order before their contents are visible to any actor, including the sequencer.

## 🌟 Key Features

- 🔐 **Privacy-First (C++)**: Leverages `libsodium` for high-speed `curve25519-xsalsa20-poly1305` encryption.
- ⏱️ **Verifiable FIFO**: Assigns immutable sequence IDs upon receipt, guaranteed by Raft consensus.
- 🛡️ **MEV Prevention**: Front-running and sandwich attacks are mathematically impossible as transaction data is encrypted during sequencing.
- 🔗 **Blockchain Commitment**: Cryptographic proofs of order are published to an Ethereum-compatible smart contract (`OrderVerifier`).
- 🚀 **Scalable Consensus**: Distributed sequencer cluster powered by the Raft consensus algorithm.

## 🏗️ Architecture

### System Overview
```mermaid
graph TD
    Client[Clients/Bots] -->|1. Encrypted Tx| Sequencer[Sequencer Cluster - Raft]
    Sequencer -->|2. Assign SeqID| Log[(Raft Replicated Log)]
    Log -->|3. FIFO Order| Mempool[Encrypted Mempool]
    Mempool -->|4. Batch Reveal| Privacy[C++ Privacy Layer]
    Privacy -->|5. Decrypt| DEX[Mock DEX Engine]
    Sequencer -->|6. Publish Commitment| EVM[Ethereum/OrderVerifier]
```

### Transaction Flow Pipeline
```mermaid
sequenceDiagram
    participant Client as Client
    participant Sequencer as Sequencer<br/>Node
    participant RaftLog as Raft<br/>Log
    participant Mempool as Encrypted<br/>Mempool
    participant Privacy as C++ Privacy<br/>Layer
    participant SmartContract as OrderVerifier<br/>Contract

    Client->>Sequencer: Submit encrypted transaction
    Sequencer->>RaftLog: Replicate & assign SeqID
    RaftLog-->>Sequencer: Ack replication
    Sequencer->>Mempool: Queue in FIFO order
    Note over Sequencer,Mempool: Immutable ordering guaranteed
    Mempool->>Privacy: Batch reveal request
    Privacy->>Privacy: Decrypt transactions
    Privacy-->>Mempool: Plaintext batch
    Mempool->>SmartContract: Publish cryptographic proof
    SmartContract-->>Mempool: Commitment recorded on-chain
```

### MEV Protection Mechanism
```mermaid
graph LR
    subgraph Traditional["Traditional MEV Risk"]
        A["TX in Mempool"] -->|Visible| B["Front-runner observes"]
        B -->|Places order| C["Sandwich Attack"]
        C -->|Extracts Value| D["MEV Lost"]
    end

    subgraph SecureOrder["Secure-Order Protection"]
        E["TX Encrypted"] -->|Hidden| F["Sequencer receives"]
        F -->|FIFO assigned| G["Immutable Order"]
        G -->|Decrypted batch| H["Execution"]
        H -->|No advantage| I["MEV Eliminated"]
    end
    
    style Traditional fill:#ff6b6b
    style SecureOrder fill:#51cf66
```

### Raft Consensus for Ordering
```mermaid
graph TB
    Client[Client] -->|Encrypted TX| Leader["Leader<br/>(Primary Sequencer)"]
    Leader -->|Replicate| Follower1["Follower 1"]
    Leader -->|Replicate| Follower2["Follower 2"]
    Follower1 -->|ACK| Leader
    Follower2 -->|ACK| Leader
    Leader -->|Assign SeqID| Log["Immutable<br/>Commit Log"]
    Log -->|Consensus| State["Shared State<br/>Across Cluster"]
    
    style Leader fill:#4dabf7
    style Follower1 fill:#a8e6cf
    style Follower2 fill:#a8e6cf
    style State fill:#ffd93d
```

### Privacy Layer: Commit-Reveal Scheme
```mermaid
graph LR
    subgraph Commit["Phase 1: Commit"]
        TX["Plaintext TX"] -->|Encrypt with<br/>curve25519-xsalsa20-poly1305| Encrypted["Encrypted TX"]
        Encrypted -->|Sequencer assigns<br/>immutable SeqID| Committed["(SeqID, Ciphertext)"]
    end
    
    subgraph Reveal["Phase 2: Reveal"]
        Committed -->|Batch window<br/>closes| Batch["Batch ready"]
        Batch -->|Decrypt all| Decrypted["Plaintext Batch"]
        Decrypted -->|Execute in<br/>SeqID order| Result["Ordered Execution"]
    end
    
    Commit --> Reveal
    style Commit fill:#e3f2fd
    style Reveal fill:#f3e5f5
```

### Component Architecture
```mermaid
graph TB
    subgraph Client["Client Layer"]
        CLI["CLI Client"]
        Bot["Trading Bots"]
    end
    
    subgraph Sequencer["Sequencer Cluster"]
        RPC["gRPC Server"]
        Consensus["Raft<br/>Consensus"]
        Queue["Encrypted<br/>Mempool"]
        Coordinator["Coordinator"]
    end
    
    subgraph Privacy["Privacy Engine"]
        CppImpl["C++ Implementation"]
        Libsodium["libsodium<br/>Crypto"]
    end
    
    subgraph Execution["Execution Layer"]
        DEX["Mock DEX<br/>Engine"]
        SmartContract["OrderVerifier<br/>Contract"]
    end
    
    CLI -->|gRPC| RPC
    Bot -->|gRPC| RPC
    RPC --> Consensus
    Consensus --> Queue
    Queue --> Coordinator
    Coordinator --> CppImpl
    CppImpl --> Libsodium
    Coordinator --> DEX
    Coordinator --> SmartContract
    
    style Client fill:#e8f5e9
    style Sequencer fill:#e3f2fd
    style Privacy fill:#f3e5f5
    style Execution fill:#fff3e0
```

### Technology Stack
```mermaid
graph TB
    subgraph Crypto["Cryptography"]
        Sodium["libsodium"]
        Curve25519["curve25519<br/>Encryption"]
        XSalsa20["XSalsa20<br/>Stream Cipher"]
        Poly1305["Poly1305<br/>Auth Tag"]
    end
    
    subgraph Consensus["Consensus"]
        Raft["Hashicorp<br/>Raft"]
        Log["Replicated<br/>Log"]
    end
    
    subgraph Sequencing["Sequencing"]
        Go["Go 1.21+"]
        gRPC["gRPC<br/>Communication"]
    end
    
    subgraph SmartContracts["Smart Contracts"]
        Solidity["Solidity"]
        Hardhat["Hardhat<br/>Framework"]
        Ethers["Ethers.js"]
    end
    
    Sodium --> Curve25519
    Curve25519 --> XSalsa20
    XSalsa20 --> Poly1305
    
    style Crypto fill:#ffe0b2
    style Consensus fill:#c8e6c9
    style Sequencing fill:#b3e5fc
    style SmartContracts fill:#f8bbd0
```

## 📊 How It Works: Step-by-Step

### The Problem We Solve
```
Traditional DEX:
User TX in Mempool → Attacker sees TX → Front-runs it → User pays premium
                                           MEV extracted = User's loss
```

```
Secure-Order:
User TX encrypted → Sequencer assigns order → TX revealed → Executed in order
                       No one sees contents → No front-running possible
```

### Execution Flow Diagram
```mermaid
graph LR
    Step1["Step 1: Client encrypts<br/>transaction"] -->
    Step2["Step 2: Sends to<br/>sequencer"] -->
    Step3["Step 3: Raft consensus<br/>assigns SeqID"] -->
    Step4["Step 4: Stored in<br/>encrypted mempool"] -->
    Step5["Step 5: Batch revealed<br/>& decrypted"] -->
    Step6["Step 6: Execute in<br/>FIFO order"] -->
    Step7["Step 7: Proof published<br/>on-chain"]
    
    style Step1 fill:#e0e7ff
    style Step2 fill:#e0e7ff
    style Step3 fill:#ddd6fe
    style Step4 fill:#dbeafe
    style Step5 fill:#fbcfe8
    style Step6 fill:#fecbcb
    style Step7 fill:#fef08a
```

### Key Guarantees
```mermaid
graph TB
    subgraph Guarantees["Secure-Order Guarantees"]
        G1["CHECK Verifiable Order<br/>via Raft Consensus"]
        G2["CHECK Private Sequencing<br/>via Encryption"]
        G3["CHECK FIFO Execution<br/>No Fairness Issues"]
        G4["CHECK On-Chain Proof<br/>Smart Contract"]
        G5["CHECK MEV Prevention<br/>No Front-Running"]
        G6["CHECK Scalable Cluster<br/>Multi-Node"]
    end
    
    style Guarantees fill:#f0fdf4
```

---

## 🛠️ Technology Stack

- **Privacy Layer**: C++17, libsodium
- **Sequencing Engine**: Go 1.21+
- **Consensus**: Hashicorp Raft
- **Smart Contracts**: Solidity, Hardhat, Ethers.js
- **Communication**: gRPC (Protobuf)

### Security Properties
```mermaid
graph LR
    A["Confidentiality<br/>Transactions Hidden<br/>Until Reveal"] --> B["Integrity<br/>Raft-Backed<br/>Order Guarantee"]
    B --> C["Availability<br/>Distributed<br/>Sequencers"]
    C --> D["Fairness<br/>FIFO Ordering<br/>No Prioritization"]
    
    style A fill:#ffcccc
    style B fill:#ccffcc
    style C fill:#ccccff
    style D fill:#ffffcc
```

---



## 🚀 Quick Start

### 1. Prerequisites

**macOS:**
```bash
brew install libsodium cmake pkg-config go node
```

**Ubuntu:**
```bash
sudo apt update && sudo apt install -y build-essential cmake libsodium-dev pkg-config golang-go nodejs npm
```

### 2. Build the System

Build the native C++ library first, then build the Go binaries.

```bash
# Build C++ Privacy Layer
cd cpp
mkdir -p build
cd build
cmake -DCMAKE_INSTALL_PREFIX=. ..
make -j$(nproc)
make install
cd ../..

# Install JS dependencies (for Smart Contracts)
npm install

# Export linker/runtime environment
export CXXFLAGS="-std=c++17"
export LDFLAGS="-L./cpp/build/lib -lprivacy -lsodium -lstdc++"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"

# Build Go binaries
./scripts/build-local.sh
```

If you want the sequencer to publish commitments to the EVM, build the sequencer with EVM support enabled:

```bash
CGO_ENABLED=1 go build -tags evm -o bin/sequencer ./cmd/sequencer
CGO_ENABLED=1 go build -o bin/client ./cmd/client
CGO_ENABLED=1 go build -o bin/rpc-loadtest ./cmd/rpc-loadtest
```

### 3. Kubernetes Deployment (Autoscaling)

The system is designed to run in a Kubernetes cluster using a `StatefulSet` for stable identities and a `HorizontalPodAutoscaler` (HPA) for load-based scaling.

```bash
# 1. Build the Docker image
docker build -t secureorder/sequencer:latest .

# 2. (Optional) Load image into local kind cluster
kind load docker-image secureorder/sequencer:latest --name secureorder-cluster

# 3. Apply manifests
kubectl apply -f k8s/service.yaml
kubectl apply -f k8s/statefulset.yaml
kubectl apply -f k8s/hpa.yaml
```

New nodes will automatically discover the leader via `sequencer-0` and join the Raft consensus group dynamically.

---

## 📜 Smart Contract Integration

The sequencer generates a cryptographic commitment for every batch of transactions. This commitment is published to the `OrderVerifier` contract on-chain to provide a proof-of-sequencing.

### Important note

For the current dependency versions in this repository, the bundled local EVM helper scripts are outdated:

- `./scripts/run-local-evm.sh` uses a Hardhat CLI flag that is not accepted by the installed Hardhat version
- `./scripts/deploy-local-order-verifier.sh` deploys to a simulated network instead of the long-running localhost RPC node used by the sequencer

Use the manual procedure below instead.

### Start a local Hardhat EVM node
```bash
mkdir -p .local/evm/logs .local/evm/pids
nohup npx hardhat node --hostname 0.0.0.0 > .local/evm/logs/hardhat-node.log 2>&1 < /dev/null &
echo $! > .local/evm/pids/hardhat.pid

# Verify the node is listening on 8545
cat .local/evm/pids/hardhat.pid
ss -ltnp | grep 8545
```

### Deploy `OrderVerifier` to the running localhost node
```bash
deploy_output="$(npx hardhat run scripts/deploy-order-verifier.ts --network localhost)"
echo "$deploy_output"

address="$(echo "$deploy_output" | awk '/OrderVerifier deployed at:/ {print $4}')"
test -n "$address"

mkdir -p .local/evm
printf '%s\n' "$address" > .local/evm/order_verifier.address
cat .local/evm/order_verifier.address
```

### Environment required for EVM settlement
```bash
export ORDER_VERIFIER_CONTRACT="$(cat .local/evm/order_verifier.address)"
export ORDER_VERIFIER_RPC_URL="http://127.0.0.1:8545"
export ORDER_VERIFIER_CHAIN_ID="31337"
export ORDER_VERIFIER_PRIVATE_KEY="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
export LD_LIBRARY_PATH="$PWD/cpp/build/lib:$LD_LIBRARY_PATH"
```

### Running the sequencer with EVM settlement
Build the EVM-enabled sequencer first:

```bash
CGO_ENABLED=1 go build -tags evm -o bin/sequencer ./cmd/sequencer
```

Then start your sequencer or Raft nodes with the environment above already exported.
Only the current Raft leader will publish `commitOrder` transactions.

### Verifying on-chain settlement
Watch the local Hardhat log for `OrderVerifier#commitOrder` calls:

```bash
tail -f .local/evm/logs/hardhat-node.log
```

---

## 📂 Project Structure

- `cmd/`: Entry points for `sequencer`, `client`, and `loadtest`.
- `pkg/`: Core logic for `privacy` (C++ wrapper) and `sequencing` (Raft/Queue).
- `cpp/`: C++ implementation of the encryption/decryption engine.
- `contracts/`: Solidity smart contracts for order verification.
- `internal/rpc/`: gRPC server implementation.
- `scripts/`: Automation scripts for deployment and testing.
- `proto/`: Protocol Buffer definitions.

## 🤝 Team
- Devarsh Doshi, Dhairya Rupani, Drumil Bhati, Prasham Mehta, Vidhan Nahar

## 📝 License
MIT License
