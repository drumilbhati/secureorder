# SecureOrder: Comprehensive Deployment Guide

## Overview

SecureOrder is a FIFO transaction sequencing system that prevents MEV through encrypted transaction ordering. This guide covers deploying the complete system across various environments.

## System Architecture

```
┌─────────────┐
│   Clients   │  (Submit encrypted transactions via gRPC)
└──────┬──────┘
       │
       ▼
┌──────────────────────────────┐
│   Go Sequencer Server        │  :50051 (gRPC)
│ ┌────────────────────────┐   │
│ │ FIFO Queue             │   │  (Sequence IDs assigned)
│ │ (thread-safe channel)  │   │
│ └────────────────────────┘   │
└──────────────┬───────────────┘
               │
               ▼
       ┌──────────────┐
       │ C++ Privacy  │  (Parallel batch decryption)
       │ Layer        │  (libsodium + OpenSSL)
       │ (libsodium)  │
       └──────────────┘
               │
               ▼
       ┌──────────────────┐
       │ Commitment       │  (SHA-256 Merkle roots)
       │ Engine           │
       └──────────────────┘
               │
               ▼
       ┌──────────────────────────────────┐
       │ Smart Contract (on-chain proof)   │
       │ Mock DEX (execution)              │
       └──────────────────────────────────┘
```

## Prerequisites

### System Requirements
- **OS**: Linux (Ubuntu 20.04+), macOS (12+), or compatible Unix
- **CPU**: 2+ cores (4+ recommended for production)
- **RAM**: 2GB minimum, 8GB+ recommended for production
- **Disk**: 10GB for dependencies and logs
- **Network**: 1 Mbps+ network connection

### Required Software
- Go 1.21 or higher
- C++ compiler (GCC 9+ or Clang 10+)
- CMake 3.10+
- libsodium (latest stable)
- Docker (optional, for containerization)
- Kubernetes (optional, for orchestration)

## Deployment Options

### Option 1: Local Development Deployment

#### 1.1 Prerequisites
```bash
# Install Go
brew install go  # macOS
# or
sudo apt-get install golang-go  # Ubuntu

# Install C++ tools
brew install cmake gcc libsodium  # macOS
# or
sudo apt-get install build-essential cmake libsodium-dev  # Ubuntu
```

#### 1.2 Build from Source
```bash
# Clone repository
git clone https://github.com/drumilbhati/secureorder.git
cd secureorder

# Build C++ library
cd cpp
mkdir -p build && cd build
cmake ..
make
cd ../..

# Build Go binaries
go mod tidy
go build -o bin/sequencer ./cmd/sequencer
go build -o bin/demo ./cmd/demo
```

#### 1.3 Run Sequencer
```bash
# Set library path
export LD_LIBRARY_PATH=$PWD/cpp/build/lib:$LD_LIBRARY_PATH

# Start server
./bin/sequencer
# Output: Secure-Order Sequencer running on 127.0.0.1:50051
```

#### 1.4 Test with Demo
```bash
# In another terminal
./bin/demo
```

---

### Option 2: Docker Deployment

#### 2.1 Create Dockerfile

Create `Dockerfile`:

```dockerfile
# Multi-stage build for optimal image size
FROM ubuntu:22.04 as builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    cmake \
    git \
    golang-go \
    libsodium-dev \
    ca-certificates

# Clone and build
WORKDIR /app
COPY . .

# Build C++ library
RUN cd cpp && mkdir -p build && cd build && \
    cmake .. && make && cd ../..

# Build Go binary
RUN go mod download
RUN CGO_ENABLED=1 GOOS=linux go build -o sequencer ./cmd/sequencer

# Runtime stage - minimal image
FROM ubuntu:22.04

RUN apt-get update && apt-get install -y \
    libsodium23 \
    ca-certificates && \
    rm -rf /var/apt/lists/*

WORKDIR /app
COPY --from=builder /app/sequencer /app/sequencer
COPY --from=builder /app/cpp/build/lib/*.so* /app/lib/

# Expose gRPC port
EXPOSE 50051

# Set library path
ENV LD_LIBRARY_PATH=/app/lib:$LD_LIBRARY_PATH

ENTRYPOINT ["/app/sequencer"]
```

#### 2.2 Build Docker Image
```bash
docker build -t secureorder:latest .
docker build -t secureorder:v1.0 .
```

#### 2.3 Run Docker Container
```bash
# Basic run
docker run -p 50051:50051 secureorder:latest

# With volume mount for logs
docker run -p 50051:50051 \
  -v /var/log/secureorder:/app/logs \
  secureorder:latest

# With resource limits
docker run -p 50051:50051 \
  --memory="2g" \
  --cpus="2" \
  secureorder:latest
```

#### 2.4 Docker Compose

Create `docker-compose.yml`:

```yaml
version: '3.8'

services:
  sequencer:
    build: .
    image: secureorder:latest
    container_name: secureorder-sequencer
    ports:
      - "50051:50051"
    environment:
      - LOG_LEVEL=info
      - GRPC_PORT=50051
    volumes:
      - ./logs:/app/logs
      - ./keys:/app/keys
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "grpcurl", "-plaintext", "localhost:50051", "list"]
      interval: 30s
      timeout: 10s
      retries: 3

  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    restart: unless-stopped

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - grafana-storage:/var/lib/grafana
    restart: unless-stopped
    depends_on:
      - prometheus

volumes:
  grafana-storage:
```

Run:
```bash
docker-compose up -d
```

---

### Option 3: Kubernetes Deployment

#### 3.1 Create Kubernetes Manifests

Create `k8s/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: secureorder-sequencer
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: secureorder-sequencer
  template:
    metadata:
      labels:
        app: secureorder-sequencer
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: sequencer
        image: secureorder:v1.0
        imagePullPolicy: Always
        ports:
        - name: grpc
          containerPort: 50051
          protocol: TCP
        - name: metrics
          containerPort: 8080
          protocol: TCP
        env:
        - name: LOG_LEVEL
          value: "info"
        - name: GRPC_PORT
          value: "50051"
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2000m"
        livenessProbe:
          grpc:
            port: 50051
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          grpc:
            port: 50051
          initialDelaySeconds: 5
          periodSeconds: 10
        volumeMounts:
        - name: keys
          mountPath: /app/keys
          readOnly: true
        - name: logs
          mountPath: /app/logs
      volumes:
      - name: keys
        secret:
          secretName: sequencer-keys
      - name: logs
        emptyDir: {}
```

Create `k8s/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: secureorder-sequencer
  namespace: default
spec:
  type: LoadBalancer
  selector:
    app: secureorder-sequencer
  ports:
  - name: grpc
    port: 50051
    targetPort: 50051
    protocol: TCP
```

Create `k8s/configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: secureorder-config
  namespace: default
data:
  sequencer-config.yaml: |
    grpc:
      port: 50051
      max_concurrent_streams: 1000
    queue:
      capacity: 10000
      batch_size: 100
    logging:
      level: info
      format: json
```

#### 3.2 Deploy to Kubernetes

```bash
# Create namespace
kubectl create namespace secureorder

# Create secrets (keys)
kubectl create secret generic sequencer-keys \
  --from-file=public.key=./keys/sequencer.pub \
  --from-file=private.key=./keys/sequencer.priv \
  -n secureorder

# Apply manifests
kubectl apply -f k8s/configmap.yaml -n secureorder
kubectl apply -f k8s/deployment.yaml -n secureorder
kubectl apply -f k8s/service.yaml -n secureorder

# Check deployment
kubectl get pods -n secureorder
kubectl logs -f deployment/secureorder-sequencer -n secureorder
```

---

### Option 4: Cloud Platform Deployment

#### 4.1 AWS Deployment (ECS + Fargate)

**Create ECR Repository:**
```bash
aws ecr create-repository --repository-name secureorder

# Build and push image
docker build -t secureorder:latest .
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin 123456789.dkr.ecr.us-east-1.amazonaws.com
docker tag secureorder:latest 123456789.dkr.ecr.us-east-1.amazonaws.com/secureorder:latest
docker push 123456789.dkr.ecr.us-east-1.amazonaws.com/secureorder:latest
```

**Create ECS Task Definition (`ecs-task-definition.json`):**
```json
{
  "family": "secureorder-sequencer",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "1024",
  "memory": "2048",
  "containerDefinitions": [
    {
      "name": "sequencer",
      "image": "123456789.dkr.ecr.us-east-1.amazonaws.com/secureorder:latest",
      "portMappings": [
        {
          "containerPort": 50051,
          "protocol": "tcp"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/secureorder",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "ecs"
        }
      },
      "environment": [
        {
          "name": "LOG_LEVEL",
          "value": "info"
        }
      ]
    }
  ]
}
```

**Create ECS Service:**
```bash
# Create CloudWatch log group
aws logs create-log-group --log-group-name /ecs/secureorder

# Register task definition
aws ecs register-task-definition \
  --cli-input-json file://ecs-task-definition.json

# Create ECS cluster
aws ecs create-cluster --cluster-name secureorder-cluster

# Create service
aws ecs create-service \
  --cluster secureorder-cluster \
  --service-name secureorder-sequencer \
  --task-definition secureorder-sequencer:1 \
  --desired-count 3 \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[subnet-xxxxx],securityGroups=[sg-xxxxx],assignPublicIp=ENABLED}"
```

#### 4.2 Google Cloud Deployment (Cloud Run)

```bash
# Set project
gcloud config set project YOUR_PROJECT_ID

# Build image
gcloud builds submit --tag gcr.io/YOUR_PROJECT_ID/secureorder

# Deploy to Cloud Run
gcloud run deploy secureorder \
  --image gcr.io/YOUR_PROJECT_ID/secureorder \
  --platform managed \
  --region us-central1 \
  --memory 2Gi \
  --cpu 2 \
  --port 50051 \
  --max-instances 10 \
  --allow-unauthenticated
```

#### 4.3 Azure Deployment (Container Instances)

```bash
# Create resource group
az group create --name secureorder-rg --location eastus

# Create container registry
az acr create --resource-group secureorder-rg \
  --name secureorderacr --sku Basic

# Build and push image
az acr build --registry secureorderacr \
  --image secureorder:latest .

# Deploy to Container Instances
az container create \
  --resource-group secureorder-rg \
  --name secureorder-sequencer \
  --image secureorderacr.azurecr.io/secureorder:latest \
  --cpu 2 --memory 2 \
  --port 50051 \
  --registry-login-server secureorderacr.azurecr.io \
  --registry-username <username> \
  --registry-password <password>
```

---

## Configuration Management

### Environment Variables

Create `.env.production`:

```bash
# Sequencer Configuration
GRPC_PORT=50051
LOG_LEVEL=info
LOG_FORMAT=json

# Queue Configuration
QUEUE_CAPACITY=10000
BATCH_SIZE=100
BATCH_TIMEOUT=5s

# Privacy Layer
KEYSTORE_PATH=/app/keys
SEQUENCER_PUBLIC_KEY_FILE=sequencer.pub
SEQUENCER_PRIVATE_KEY_FILE=sequencer.priv

# Security
TLS_ENABLED=true
TLS_CERT_FILE=/app/certs/server.crt
TLS_KEY_FILE=/app/certs/server.key

# Monitoring
METRICS_ENABLED=true
METRICS_PORT=8080

# Smart Contract
CONTRACT_ADDRESS=0x...
CONTRACT_RPC_URL=http://localhost:8545
```

### Configuration File

Create `config.yaml`:

```yaml
server:
  grpc_port: 50051
  http_port: 8080
  max_connections: 1000
  keepalive_time: 20s

queue:
  capacity: 10000
  batch_size: 100
  batch_timeout: 5s

privacy:
  keystore_path: /app/keys
  algorithm: curve25519-xsalsa20-poly1305
  thread_pool_size: 4

logging:
  level: info
  format: json
  output: stdout

monitoring:
  enabled: true
  prometheus_port: 8080

security:
  tls:
    enabled: true
    cert_path: /app/certs/server.crt
    key_path: /app/certs/server.key
  rate_limiting:
    enabled: true
    requests_per_second: 1000
```

---

## Security Hardening

### 1. Key Management

```bash
# Generate keys securely
openssl rand -out random_seed 32
secureorder keygen --seed random_seed --output-dir ./keys

# Secure permissions
chmod 400 ./keys/sequencer.priv
chmod 444 ./keys/sequencer.pub

# Store in Kubernetes secrets
kubectl create secret generic sequencer-keys \
  --from-file=private.key=./keys/sequencer.priv \
  --from-file=public.key=./keys/sequencer.pub
```

### 2. Network Security

**firewall.rules:**
```
# Allow gRPC traffic only from approved clients
-A INPUT -p tcp --dport 50051 -s 10.0.0.0/8 -j ACCEPT
-A INPUT -p tcp --dport 50051 -j DROP

# Allow metrics only from Prometheus
-A INPUT -p tcp --dport 8080 -s 10.0.1.0/24 -j ACCEPT
```

### 3. TLS/SSL Configuration

```bash
# Generate self-signed certificate for testing
openssl req -x509 -newkey rsa:4096 \
  -keyout server.key -out server.crt \
  -days 365 -nodes \
  -subj "/CN=secureorder.example.com"

# In production, use proper certificates from CA:
certbot certonly --standalone -d secureorder.example.com
```

### 4. Container Security

```dockerfile
# Run as non-root user
RUN useradd -m -u 1000 sequencer
USER sequencer

# Read-only filesystem where possible
RUN chmod -R 555 /app/bin
```

---

## Monitoring & Logging

### 1. Prometheus Metrics

Add to `cmd/sequencer/main.go`:

```go
import "github.com/prometheus/client_golang/prometheus"

var (
    txnCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "secureorder_transactions_total",
            Help: "Total transactions processed",
        },
        []string{"status"},
    )
    queueSize = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "secureorder_queue_size",
            Help: "Current queue size",
        },
    )
    decryptLatency = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name: "secureorder_decrypt_latency_seconds",
            Help: "Decryption latency in seconds",
        },
    )
)
```

### 2. Logging

```go
import "go.uber.org/zap"

logger, _ := zap.NewProduction()
defer logger.Sync()

logger.Info("transaction submitted",
    zap.String("user", userAddress),
    zap.Uint64("seq_id", seqID),
)
```

### 3. Health Checks

```bash
# gRPC health check
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check

# HTTP health check
curl http://localhost:8080/health

# Kubernetes health probe
kubectl exec -it <pod> -- grpcurl -plaintext localhost:50051 list
```

---

## Scaling Strategies

### Horizontal Scaling
- Run multiple sequencer instances
- Use load balancer (AWS NLB, GCP Load Balancer, nginx)
- Share queue state via distributed system (Redis, etcd)

### Vertical Scaling
- Increase CPU cores for parallel decryption
- Increase RAM for larger queue capacity
- Tune thread pool size

### Database Backend
If persistence is needed:

```bash
# PostgreSQL for transaction history
docker run -p 5432:5432 \
  -e POSTGRES_DB=secureorder \
  -e POSTGRES_PASSWORD=secret \
  postgres:15

# Connect from Go:
// db.go
db, _ := sql.Open("postgres", "postgres://user:pass@localhost/secureorder")
```

---

## Smart Contract Deployment

### 1. Deploy Order Commitment Contract

```bash
# Using Hardhat
cd contracts
npm install
npx hardhat compile
npx hardhat deploy --network testnet

# Or with Foundry
forge install
forge build
forge deploy src/OrderCommitment.sol
```

### 2. Update Sequencer Configuration

```yaml
smart_contract:
  address: "0x..."
  rpc_url: "https://testnet.example.com:8545"
  private_key: "${DEPLOYER_PRIVATE_KEY}"
  chain_id: 5
```

---

## Backup & Recovery

### Database Backups

```bash
# PostgreSQL backup
pg_dump -U sequencer -h localhost secureorder > backup.sql

# Restore
psql -U sequencer -h localhost secureorder < backup.sql

# Automated daily backup (crontab)
0 2 * * * pg_dump -U sequencer secureorder > /backups/secureorder-$(date +\%Y\%m\%d).sql
```

### Key Backups

```bash
# Encrypt and backup keys
gpg --symmetric --cipher-algo aes256 keys/sequencer.priv
# Store in secure location (AWS Secrets Manager, HashiCorp Vault)
```

---

## Disaster Recovery Plan

### RTO/RPO Targets
- **RTO** (Recovery Time Objective): 5 minutes
- **RPO** (Recovery Point Objective): 1 minute

### Recovery Procedures

1. **Single Instance Failure**: Kubernetes automatically replaces pod
2. **Data Corruption**: Restore from backup
3. **Key Compromise**: Rotate keys, redeploy contracts
4. **Entire Region Down**: Failover to secondary region

---

## Performance Tuning

### Queue Settings

```yaml
queue:
  capacity: 100000        # Larger buffer
  batch_size: 1000        # Process more at once
  batch_timeout: 1s       # Shorter timeout
  worker_threads: 16      # Match CPU cores
```

### C++ Thread Pool

```c++
// Tune thread pool size
size_t num_threads = std::thread::hardware_concurrency() * 2;
ThreadPool pool(num_threads);
```

### gRPC Settings

```go
import "google.golang.org/grpc/keepalive"

s := grpc.NewServer(
    grpc.KeepaliveParams(keepalive.ServerParameters{
        MaxConnectionIdle:     5 * time.Minute,
        MaxConnectionAge:      2 * time.Hour,
        MaxConnectionAgeGrace: 5 * time.Minute,
        Time:                  2 * time.Hour,
        Timeout:               20 * time.Second,
    }),
    grpc.MaxConcurrentStreams(1000),
)
```

---

## Troubleshooting Deployment Issues

### Issue: Container won't start

```bash
# Check logs
docker logs -f <container_id>

# Verify image
docker inspect <image>

# Test locally
docker run -it --rm -p 50051:50051 secureorder:latest /bin/bash
```

### Issue: gRPC connection refused

```bash
# Check port listening
netstat -tlnp | grep 50051

# Check firewall
sudo iptables -L -n | grep 50051

# Test connectivity
grpcurl -plaintext localhost:50051 list
```

### Issue: High latency

```bash
# Monitor CPU/memory
docker stats <container_id>

# Increase resources in docker-compose
resources:
  limits:
    cpus: '4'
    memory: 8G
```

---

## Maintenance & Upgrades

### Rolling Upgrade

```bash
# Kubernetes automatically handles rolling updates
kubectl set image deployment/secureorder-sequencer \
  sequencer=secureorder:v1.1 \
  --record

# Monitor rollout
kubectl rollout status deployment/secureorder-sequencer
```

### Downtime Upgrade

```bash
# Stop service
docker-compose down

# Backup data
cp -r ./data ./data.backup

# Update and restart
git pull
docker-compose build
docker-compose up -d
```

---

## Compliance & Auditing

### Audit Logging

```go
// Log all transactions for compliance
auditLog.Info("transaction_sequenced",
    zap.String("user_id", userID),
    zap.Uint64("seq_id", seqID),
    zap.Time("timestamp", time.Now()),
)
```

### Regulatory Compliance
- GDPR: Ensure data retention policies
- SOC 2: Implement monitoring and logging
- PCI DSS: Encrypt keys in transit and at rest

---

## Cost Optimization

### AWS Cost Estimate (Monthly)
- Fargate: $50-200 (depending on vCPU/memory)
- Data Transfer: $0-50
- Logs (CloudWatch): $10-30
- RDS (optional): $50-300
- **Total: ~$150-600**

### GCP Cost Estimate (Monthly)
- Cloud Run: $20-100
- Cloud Logging: $5-20
- Cloud SQL (optional): $50-300
- **Total: ~$75-420**

### Cost Reduction Tips
1. Use spot/preemptible instances
2. Implement auto-scaling based on load
3. Optimize image size (multi-stage builds)
4. Use managed services instead of self-hosted

---

## Conclusion

SecureOrder can be deployed in multiple ways depending on your needs:

- **Development**: Local build + run
- **Testing**: Docker containers
- **Production**: Kubernetes on managed cloud (AWS/GCP/Azure)
- **High-Scale**: Multi-region with failover

Start with Docker, then migrate to Kubernetes for better scalability and reliability.

For questions or issues, refer to the project documentation or open an issue on GitHub.
