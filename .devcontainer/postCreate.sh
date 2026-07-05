#!/usr/bin/env bash
set -euo pipefail

echo "==> Installing kind (Kubernetes in Docker)"
KIND_VERSION="v0.23.0"
curl -Lo ./kind "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-amd64"
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind

echo "==> Verifying kubectl and helm"
kubectl version --client
helm version

echo "==> Fetching Hyperledger Fabric samples, binaries, and Docker images"
mkdir -p ~/fabric && cd ~/fabric
curl -sSL https://raw.githubusercontent.com/hyperledger/fabric/main/scripts/install-fabric.sh -o install-fabric.sh
chmod +x install-fabric.sh
./install-fabric.sh docker samples binary

echo "==> Setup complete."
echo "Next steps:"
echo "  1. kind create cluster --name dev"
echo "  2. cd ~/fabric/fabric-samples/test-network && ./network.sh up createChannel"
