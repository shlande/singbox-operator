/*
Copyright 2026.

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

package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-singboxoperator-shlande-top-v1alpha1-egresspolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=singboxoperator.shlande.top,resources=egresspolicies,verbs=create;update,versions=v1alpha1,name=vegresspolicy-v1alpha1.kb.io,admissionReviewVersions=v1

type EgressPolicyWebhook struct{}

func (w *EgressPolicyWebhook) ValidateCreate(ctx context.Context, policy *v1alpha1.EgressPolicy) (admission.Warnings, error) {
	return nil, validateEgressPolicy(policy)
}

func (w *EgressPolicyWebhook) ValidateUpdate(ctx context.Context, oldPolicy, policy *v1alpha1.EgressPolicy) (admission.Warnings, error) {
	return nil, validateEgressPolicy(policy)
}

func (w *EgressPolicyWebhook) ValidateDelete(ctx context.Context, policy *v1alpha1.EgressPolicy) (admission.Warnings, error) {
	return nil, nil
}

func validateEgressPolicy(policy *v1alpha1.EgressPolicy) error {
	var errs field.ErrorList
	if policy.Spec.Action != v1alpha1.EgressPolicyActionRoute && policy.Spec.Action != v1alpha1.EgressPolicyActionReject {
		errs = append(errs, field.NotSupported(field.NewPath("spec", "action"), policy.Spec.Action, []string{v1alpha1.EgressPolicyActionRoute, v1alpha1.EgressPolicyActionReject}))
	}
	if err := policy.Spec.IngressSelector.Valid(); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec", "ingressSelector"), policy.Spec.IngressSelector, err.Error()))
	}
	if err := policy.Spec.EgressSelector.Valid(); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec", "egressSelector"), policy.Spec.EgressSelector, err.Error()))
	}
	if err := policy.Spec.Match.Valid(policy.Spec.Fallback); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec", "match"), policy.Spec.Match, err.Error()))
	}
	if policy.Spec.Action == v1alpha1.EgressPolicyActionRoute && len(policy.Spec.EgressSelector.MatchNames) == 0 && len(policy.Spec.EgressSelector.MatchLabels) == 0 && len(policy.Spec.EgressSelector.MatchExpressions) == 0 {
		errs = append(errs, field.Required(field.NewPath("spec", "egressSelector"), "egressSelector is required for route actions"))
	}
	return errs.ToAggregate()
}

func SetupEgressPolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.EgressPolicy{}).
		WithValidator(&EgressPolicyWebhook{}).
		Complete()
}
