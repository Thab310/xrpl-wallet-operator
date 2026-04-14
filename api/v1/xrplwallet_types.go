/*
Copyright 2026 Thabelo Ramabulana.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition type constants for XRPLWallet.
const (
	// ConditionTypeReady indicates the wallet is fully provisioned and ready.
	ConditionTypeReady = "Ready"
	// ConditionTypeSecretCreated indicates the K8s Secret has been created.
	ConditionTypeSecretCreated = "SecretCreated"
	// ConditionTypeFunded indicates the wallet has been funded from the faucet.
	ConditionTypeFunded = "Funded"
)

// State constants for XRPLWallet lifecycle.
const (
	StatePending  = "Pending"
	StateCreating = "Creating"
	StateFunding  = "Funding"
	StateReady    = "Ready"
	StateError    = "Error"
	StateDeleting = "Deleting"
)

// XRPLWalletSpec defines the desired state of XRPLWallet.
type XRPLWalletSpec struct {
	// Network specifies which XRPL network to create the wallet on.
	// +kubebuilder:validation:Enum=testnet;devnet;mainnet
	// +kubebuilder:default=testnet
	Network string `json:"network"`

	// Fund controls whether the wallet should be funded from the testnet/devnet faucet.
	// Has no effect on mainnet.
	// +kubebuilder:default=true
	// +optional
	Fund *bool `json:"fund,omitempty"`

	// FundAmount is the amount of XRP to request from the faucet (testnet/devnet only).
	// The faucet typically provides ~1000 XRP regardless of this value.
	// +kubebuilder:default=1000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	// +optional
	FundAmount int `json:"fundAmount,omitempty"`

	// SecretName is the name of the Kubernetes Secret that will store the wallet credentials.
	// Defaults to the XRPLWallet resource name if not specified.
	// +kubebuilder:default="xrpl-wallet"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// SecretLabels are extra labels applied to the created Secret so workloads can discover it.
	// +optional
	SecretLabels map[string]string `json:"secretLabels,omitempty"`
}

// XRPLWalletStatus defines the observed state of XRPLWallet.
type XRPLWalletStatus struct {
	// State is the current phase of the wallet lifecycle.
	// +kubebuilder:validation:Enum=Pending;Creating;Funding;Ready;Error;Deleting
	// +optional
	State string `json:"state,omitempty"`

	// Address is the public XRPL wallet address (safe to display and share).
	// +optional
	Address string `json:"address,omitempty"`

	// Balance is the current XRP balance of the wallet as reported by the ledger.
	// +optional
	Balance string `json:"balance,omitempty"`

	// SecretCreated indicates whether the Kubernetes Secret holding credentials has been created.
	// +optional
	SecretCreated bool `json:"secretCreated,omitempty"`

	// SecretRef is the name of the Kubernetes Secret that holds the wallet credentials.
	// +optional
	SecretRef string `json:"secretRef,omitempty"`

	// Network is the XRPL network this wallet was created on.
	// +optional
	Network string `json:"network,omitempty"`

	// ExplorerURL is a link to the XRPL explorer page for this wallet address.
	// +optional
	ExplorerURL string `json:"explorerURL,omitempty"`

	// LastBalanceCheck is the timestamp of the most recent balance query.
	// +optional
	LastBalanceCheck *metav1.Time `json:"lastBalanceCheck,omitempty"`

	// Message is a human-readable description of the current state or last error.
	// +optional
	Message string `json:"message,omitempty"`

	// RetryCount tracks consecutive reconciliation failures for back-off logic.
	// +optional
	RetryCount int `json:"retryCount,omitempty"`

	// Conditions represent the latest available observations of the XRPLWallet state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=xw,categories=xrpl
// +kubebuilder:printcolumn:name="Network",type=string,JSONPath=`.spec.network`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="Balance",type=string,JSONPath=`.status.balance`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// XRPLWallet is the Schema for the xrplwallets API.
// It provisions an XRPL wallet and stores the credentials in a Kubernetes Secret.
type XRPLWallet struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of XRPLWallet.
	// +required
	Spec XRPLWalletSpec `json:"spec"`

	// status defines the observed state of XRPLWallet.
	// +optional
	Status XRPLWalletStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// XRPLWalletList contains a list of XRPLWallet resources.
type XRPLWalletList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []XRPLWallet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&XRPLWallet{}, &XRPLWalletList{})
}
