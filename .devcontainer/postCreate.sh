#!/usr/bin/env bash
set -euo pipefail

echo "==> Installing kind (Kubernetes in Docker)"
KIND_VERSION="v0.23.0"
curl -Lo ./kind "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-amd64"
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind

echo "==> Installing golangci-lint"
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b "$(go env GOPATH)/bin" 2>/dev/null || echo "golangci-lint install skipped"

echo "==> Installing Claude Code"
curl -fsSL https://claude.ai/install.sh | bash || npm install -g @anthropic-ai/claude-code

echo "==> Verifying toolchain"
kubectl version --client
helm version
go version

# Legacy Hyperledger Fabric bootstrap from the original devdesk setup.
# Not needed for rewarm; opt in with INSTALL_FABRIC=1 if you still use it.
if [ "${INSTALL_FABRIC:-0}" = "1" ]; then
  echo "==> Fetching Hyperledger Fabric samples, binaries, and Docker images"
  mkdir -p ~/fabric && cd ~/fabric
  curl -sSL https://raw.githubusercontent.com/hyperledger/fabric/main/scripts/install-fabric.sh -o install-fabric.sh
  chmod +x install-fabric.sh
  ./install-fabric.sh docker samples binary
fi

echo "==> Setup complete."
echo "Next steps:"
echo "  1. kind create cluster --name rewarm-dev   # local K8s for controller/envtest work"
echo "  2. claude                                    # CLAUDE.md loads automatically"
