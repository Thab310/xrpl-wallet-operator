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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	xrplv1 "github.com/Thab310/xrpl-wallet-operator/api/v1"
)

const (
	finalizerName        = "xrpl.thabelo.dev/wallet-finalizer"
	balanceCheckInterval = 5 * time.Minute
	maxRetries           = 5
)

// XRPLWalletReconciler reconciles XRPLWallet objects.
type XRPLWalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=xrpl.thabelo.dev,resources=xrplwallets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=xrpl.thabelo.dev,resources=xrplwallets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=xrpl.thabelo.dev,resources=xrplwallets/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *XRPLWalletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling XRPLWallet", "name", req.NamespacedName)

	// Always fetch a fresh copy to avoid "object has been modified" conflicts.
	wallet := &xrplv1.XRPLWallet{}
	if err := r.Get(ctx, req.NamespacedName, wallet); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching XRPLWallet: %w", err)
	}

	// Handle deletion.
	if !wallet.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, wallet)
	}

	// Ensure finalizer.
	if !controllerutil.ContainsFinalizer(wallet, finalizerName) {
		patch := client.MergeFrom(wallet.DeepCopy())
		controllerutil.AddFinalizer(wallet, finalizerName)
		if err := r.Patch(ctx, wallet, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Guard max retries.
	if wallet.Status.RetryCount >= maxRetries {
		return r.setError(ctx, wallet, "max retries exceeded; delete and recreate to retry")
	}

	switch wallet.Status.State {
	case "", xrplv1.StatePending:
		return r.handlePending(ctx, wallet)
	case xrplv1.StateCreating:
		return r.handleCreating(ctx, wallet)
	case xrplv1.StateFunding:
		return r.handleFunding(ctx, wallet)
	case xrplv1.StateReady:
		return r.handleReady(ctx, wallet)
	case xrplv1.StateError:
		return ctrl.Result{}, nil
	default:
		return r.transitionState(ctx, wallet, xrplv1.StatePending, "unknown state reset")
	}
}

// ── phase handlers ────────────────────────────────────────────────────────────

func (r *XRPLWalletReconciler) handlePending(ctx context.Context, wallet *xrplv1.XRPLWallet) (ctrl.Result, error) {
	wallet.Status.Network = wallet.Spec.Network
	wallet.Status.RetryCount = 0
	return r.transitionState(ctx, wallet, xrplv1.StateCreating, "starting wallet creation")
}

func (r *XRPLWalletReconciler) handleCreating(ctx context.Context, wallet *xrplv1.XRPLWallet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	secretName := wallet.Spec.SecretName
	if secretName == "" {
		secretName = wallet.Name
	}

	// Check if secret already exists (idempotency — handles controller restarts).
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: wallet.Namespace}, existingSecret)

	var address string

	if errors.IsNotFound(err) {
		// Generate fresh wallet credentials.
		xrplClient, clientErr := NewXRPLClient(wallet.Spec.Network)
		if clientErr != nil {
			return r.incrementRetry(ctx, wallet, fmt.Sprintf("creating XRPL client: %v", clientErr))
		}
		creds, clientErr := xrplClient.GenerateWallet()
		if clientErr != nil {
			return r.incrementRetry(ctx, wallet, fmt.Sprintf("generating wallet: %v", clientErr))
		}

		secret := r.buildSecret(wallet, secretName, creds)
		if createErr := r.Create(ctx, secret); createErr != nil {
			return r.incrementRetry(ctx, wallet, fmt.Sprintf("creating secret: %v", createErr))
		}
		address = creds.Address
		logger.Info("Created wallet secret", "secret", secretName, "address", address)
	} else if err != nil {
		return r.incrementRetry(ctx, wallet, fmt.Sprintf("checking secret: %v", err))
	} else {
		// Recover address from an existing secret.
		address = string(existingSecret.Data["address"])
		logger.Info("Recovered existing wallet", "address", address)
	}

	xrplClient, _ := NewXRPLClient(wallet.Spec.Network)

	// Re-fetch to get the latest resourceVersion before patching status.
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, err
	}

	wallet.Status.Address = address
	wallet.Status.SecretCreated = true
	wallet.Status.SecretRef = secretName
	wallet.Status.ExplorerURL = xrplClient.ExplorerURL(address)
	wallet.Status.RetryCount = 0
	meta.SetStatusCondition(&wallet.Status.Conditions, metav1.Condition{
		Type:               xrplv1.ConditionTypeSecretCreated,
		Status:             metav1.ConditionTrue,
		Reason:             "SecretCreated",
		Message:            fmt.Sprintf("credentials stored in secret/%s", secretName),
		LastTransitionTime: metav1.Now(),
	})

	shouldFund := wallet.Spec.Fund == nil || *wallet.Spec.Fund
	if shouldFund && wallet.Spec.Network != "mainnet" {
		return r.transitionState(ctx, wallet, xrplv1.StateFunding, "wallet created; requesting faucet funding")
	}
	return r.transitionState(ctx, wallet, xrplv1.StateReady, "wallet created; funding skipped")
}

func (r *XRPLWalletReconciler) handleFunding(ctx context.Context, wallet *xrplv1.XRPLWallet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	xrplClient, err := NewXRPLClient(wallet.Spec.Network)
	if err != nil {
		return r.incrementRetry(ctx, wallet, fmt.Sprintf("creating XRPL client: %v", err))
	}

	if err := xrplClient.FundWallet(ctx, wallet.Status.Address); err != nil {
		return r.incrementRetry(ctx, wallet, fmt.Sprintf("funding wallet: %v", err))
	}

	logger.Info("Wallet funded via faucet", "address", wallet.Status.Address)

	// Re-fetch before status update.
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&wallet.Status.Conditions, metav1.Condition{
		Type:               xrplv1.ConditionTypeFunded,
		Status:             metav1.ConditionTrue,
		Reason:             "FaucetFunded",
		Message:            "wallet funded from faucet",
		LastTransitionTime: metav1.Now(),
	})
	wallet.Status.RetryCount = 0
	return r.transitionState(ctx, wallet, xrplv1.StateReady, "wallet funded and ready")
}

func (r *XRPLWalletReconciler) handleReady(ctx context.Context, wallet *xrplv1.XRPLWallet) (ctrl.Result, error) {
	now := metav1.Now()

	if wallet.Status.LastBalanceCheck != nil &&
		now.Time.Sub(wallet.Status.LastBalanceCheck.Time) < balanceCheckInterval {
		return ctrl.Result{RequeueAfter: balanceCheckInterval}, nil
	}

	xrplClient, err := NewXRPLClient(wallet.Spec.Network)
	if err != nil {
		log.FromContext(ctx).Error(err, "creating XRPL client for balance check")
		return ctrl.Result{RequeueAfter: balanceCheckInterval}, nil
	}

	balance, err := xrplClient.GetBalance(ctx, wallet.Status.Address)
	if err != nil {
		log.FromContext(ctx).Error(err, "fetching balance")
		return ctrl.Result{RequeueAfter: balanceCheckInterval}, nil
	}

	// Re-fetch before patching status.
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, err
	}

	wallet.Status.Balance = balance
	wallet.Status.LastBalanceCheck = &now
	meta.SetStatusCondition(&wallet.Status.Conditions, metav1.Condition{
		Type:               xrplv1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "WalletReady",
		Message:            fmt.Sprintf("balance: %s", balance),
		LastTransitionTime: now,
	})

	if err := r.Status().Update(ctx, wallet); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating balance status: %w", err)
	}
	return ctrl.Result{RequeueAfter: balanceCheckInterval}, nil
}

func (r *XRPLWalletReconciler) handleDeletion(ctx context.Context, wallet *xrplv1.XRPLWallet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(wallet, finalizerName) {
		if wallet.Status.SecretRef != "" {
			secret := &corev1.Secret{}
			err := r.Get(ctx, types.NamespacedName{Name: wallet.Status.SecretRef, Namespace: wallet.Namespace}, secret)
			if err == nil {
				if delErr := r.Delete(ctx, secret); delErr != nil && !errors.IsNotFound(delErr) {
					return ctrl.Result{}, fmt.Errorf("deleting secret: %w", delErr)
				}
				logger.Info("Deleted wallet secret", "secret", wallet.Status.SecretRef)
			}
		}

		patch := client.MergeFrom(wallet.DeepCopy())
		controllerutil.RemoveFinalizer(wallet, finalizerName)
		if err := r.Patch(ctx, wallet, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// transitionState updates status and re-fetches first to avoid conflicts.
func (r *XRPLWalletReconciler) transitionState(ctx context.Context, wallet *xrplv1.XRPLWallet, state, message string) (ctrl.Result, error) {
	// Carry over the fields we want to keep, then re-fetch the latest version.
	savedStatus := wallet.Status
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-fetching before status update: %w", err)
	}
	// Restore the fields we computed, then overlay the new state.
	wallet.Status = savedStatus
	wallet.Status.State = state
	wallet.Status.Message = message

	if err := r.Status().Update(ctx, wallet); err != nil {
		return ctrl.Result{}, fmt.Errorf("transitioning to %s: %w", state, err)
	}
	return ctrl.Result{Requeue: true}, nil
}

// setError moves the wallet to the Error state.
func (r *XRPLWalletReconciler) setError(ctx context.Context, wallet *xrplv1.XRPLWallet, message string) (ctrl.Result, error) {
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, err
	}
	wallet.Status.State = xrplv1.StateError
	wallet.Status.Message = message
	meta.SetStatusCondition(&wallet.Status.Conditions, metav1.Condition{
		Type:               xrplv1.ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             "Error",
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, wallet); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting error state: %w", err)
	}
	return ctrl.Result{}, nil
}

// incrementRetry bumps retry count and re-queues with back-off.
func (r *XRPLWalletReconciler) incrementRetry(ctx context.Context, wallet *xrplv1.XRPLWallet, message string) (ctrl.Result, error) {
	newRetry := wallet.Status.RetryCount + 1
	backoff := time.Duration(newRetry) * 10 * time.Second

	log.FromContext(ctx).Info("Reconciliation error, will retry",
		"message", message, "retry", newRetry, "backoff", backoff)

	// Re-fetch to avoid conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: wallet.Name, Namespace: wallet.Namespace}, wallet); err != nil {
		return ctrl.Result{}, err
	}
	wallet.Status.RetryCount = newRetry
	wallet.Status.Message = message
	if err := r.Status().Update(ctx, wallet); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating retry count: %w", err)
	}
	return ctrl.Result{RequeueAfter: backoff}, nil
}

// buildSecret constructs the corev1.Secret for wallet credentials.
func (r *XRPLWalletReconciler) buildSecret(wallet *xrplv1.XRPLWallet, name string, creds *WalletCredentials) *corev1.Secret {
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "xrpl-wallet-operator",
		"xrpl.thabelo.dev/wallet":      wallet.Name,
		"xrpl.thabelo.dev/network":     wallet.Spec.Network,
	}
	for k, v := range wallet.Spec.SecretLabels {
		labels[k] = v
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: wallet.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"address":    []byte(creds.Address),
			"publicKey":  []byte(creds.PublicKey),
			"privateKey": []byte(creds.PrivateKey),
			"seed":       []byte(creds.Seed),
			"network":    []byte(creds.Network),
		},
	}
	_ = controllerutil.SetControllerReference(wallet, secret, r.Scheme)
	return secret
}

// SetupWithManager registers the controller with the manager.
func (r *XRPLWalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&xrplv1.XRPLWallet{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
