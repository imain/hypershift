package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// Webhook implements a validating webhook for NodePool.
type Webhook struct{}

// SetupWebhookWithManager sets up NodePool webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		WithValidator(&Webhook{}).
		Complete()
}

var _ webhook.CustomValidator = &Webhook{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	nodePool, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", obj))
	}

	return validateNodePoolCreate(nodePool)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	newNP, ok := newObj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", newObj))
	}

	oldNP, ok := oldObj.(*hyperv1.NodePool)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a NodePool but got a %T", oldObj))
	}

	return validateNodePoolUpdate(newNP, oldNP)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (webhook *Webhook) ValidateDelete(_ context.Context, obj runtime.Object) error {
	return nil
}

func validateNodePoolCreate(hc *hyperv1.NodePool) error {
	var errs field.ErrorList

	return errs.ToAggregate()
}

func validateNodePoolUpdate(newNP *hyperv1.NodePool, oldNP *hyperv1.NodePool) error {
	var errs field.ErrorList

	errs = append(errs, field.Invalid(field.NewPath("spec.testing"), oldNP, "just plain wrong"))
	return errs.ToAggregate()
}
