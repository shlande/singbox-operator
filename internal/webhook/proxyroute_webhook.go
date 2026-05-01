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

// ProxyRouteWebhook handles validation for ProxyRoute.
type ProxyRouteWebhook struct{}

func (w *ProxyRouteWebhook) ValidateCreate(ctx context.Context, route *v1alpha1.ProxyRoute) (admission.Warnings, error) {
	return nil, validateProxyRoute(route)
}

func (w *ProxyRouteWebhook) ValidateUpdate(ctx context.Context, oldRoute, newRoute *v1alpha1.ProxyRoute) (admission.Warnings, error) {
	return nil, validateProxyRoute(newRoute)
}

func (w *ProxyRouteWebhook) ValidateDelete(ctx context.Context, route *v1alpha1.ProxyRoute) (admission.Warnings, error) {
	return nil, nil
}

func validateProxyRoute(route *v1alpha1.ProxyRoute) error {
	var allErrs field.ErrorList

	if route.Spec.InboundNode == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "inboundNode"), "inboundNode must not be empty"))
	}

	if route.Spec.OutboundNode == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "outboundNode"), "outboundNode must not be empty"))
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

// SetupProxyRouteWebhookWithManager registers the webhook with the manager.
func SetupProxyRouteWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.ProxyRoute{}).
		WithValidator(&ProxyRouteWebhook{}).
		Complete()
}
