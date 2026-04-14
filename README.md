# XRPL Wallet Operator

A Kubernetes Operator for declaratively provisioning and managing XRP Ledger (XRPL) wallets.

Built with Kubebuilder and Go, this operator allows you to create, fund, and monitor XRPL wallets directly from Kubernetes resources.

---

## 🚀 Overview

The XRPL Wallet Operator introduces a custom resource called `XRPLWallet` that enables:

- Automatic wallet generation (ed25519 keypair)
- Secure storage of credentials in Kubernetes Secrets
- Optional funding via XRPL testnet/devnet faucet
- Continuous balance monitoring
- Full lifecycle management with cleanup on deletion

This brings blockchain wallet management into a **cloud-native, declarative workflow**.

---

## 🧠 What is an XRPL Wallet?

An **XRPL wallet** is a cryptographic identity on the XRP Ledger used to:

- Send and receive XRP  
- Sign and submit transactions  
- Interact with XRPL-based applications  

Each wallet includes:

- **Address** (public)  
- **Public key**  
- **Private key / seed** (sensitive)  

This operator securely manages these credentials using Kubernetes Secrets.

---

## ✨ Features

- 🔑 Wallet generation using ed25519
- 🔐 Secure credential storage via Kubernetes Secrets
- 💸 Faucet-based funding (testnet/devnet)
- 🔄 Continuous reconciliation loop
- 📊 Status reporting (address, balance, conditions)
- ♻️ Idempotent design (safe across restarts)
- 🧹 Finalizers for cleanup on deletion
- ⚠️ Retry logic with backoff

---

## 🧩 Custom Resource Example

```yaml
apiVersion: xrpl.thabelo.dev/v1
kind: XRPLWallet
metadata:
  name: my-testnet-wallet
  namespace: default
spec:
  network: testnet       # testnet | devnet | mainnet
  fund: true             # auto-fund via faucet (non-mainnet only)
  fundAmount: 1000       # faucet may override
  secretName: my-xrpl-secret
  secretLabels:
    app: my-app
    env: dev
```

## 🔄 Lifecycle


Pending → Creating → Funding → Ready
                       ↓
                     Error


- **Pending**: Initial state  
- **Creating**: Wallet + Secret creation  
- **Funding**: Faucet funding (if enabled)  
- **Ready**: Wallet active and monitored  
- **Error**: Retry limit reached  

---

## 🏗️ Architecture

- **CRD**: Defines `XRPLWallet`  
- **Controller**:
  - Watches resources  
  - Reconciles desired vs actual state  
- **Secrets**:
  - Store wallet credentials  
- **Finalizers**:
  - Ensure cleanup on deletion  

---

## ⚙️ Prerequisites

- Go 1.26+  
- Docker Desktop  
- kubectl  
- k3d (or any Kubernetes cluster)  
- make  

---

## 🧪 Running Locally (k3d)

This project can be run fully locally with no cloud dependencies.

### 1. Create a Kubernetes cluster

```bash
k3d cluster create xrpl-operator
```

### 2. Install CRDs

    make install

### 3. Run the controller locally

    make run

### 4. Apply a sample wallet

    kubectl apply -f config/samples/xrpl_v1_xrplwallet.yaml

### 5. Inspect resources

    kubectl get xrplwallets
    kubectl describe xrplwallet my-testnet-wallet

### 6. View generated secret

    kubectl get secret my-xrpl-secret -o yaml

---

## 🔐 Security Considerations

- Secrets contain sensitive wallet credentials  
- Use RBAC to restrict access  
- Avoid using mainnet wallets without proper secret management  
- Consider external secret stores (e.g., HashiCorp Vault)  

---

## 💡 Use Cases

- Web3 applications on Kubernetes  
- Fintech platforms needing wallet automation  
- XRPL development/testing environments  
- Blockchain CI/CD pipelines  

---

## 🚧 Future Improvements

- Integration with external secret managers  
- Transaction signing capabilities  
- Multi-wallet orchestration  
- Metrics and observability dashboards  

---

## 🛠️ Development

### Generate manifests

    make manifests

### Format and vet code

    make fmt vet

### Run tests

    make test

---

## 📦 Build and Deploy Controller (Optional)

### Build image

    make docker-build IMG=<your-image>

### Push image

    make docker-push IMG=<your-image>

### Deploy to cluster

    make deploy IMG=<your-image>

---

## 📜 License

Licensed under the Apache License, Version 2.0.

---

## 🤝 Contributing

Contributions, issues, and feature requests are welcome.

Feel free to fork the repo and submit a PR.