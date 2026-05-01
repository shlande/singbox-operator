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

// ProxyUserWebhook handles validation for ProxyUser.
type ProxyUserWebhook struct{}

func (w *ProxyUserWebhook) ValidateCreate(ctx context.Context, user *v1alpha1.ProxyUser) (admission.Warnings, error) {
	return nil, validateProxyUser(user)
}

func (w *ProxyUserWebhook) ValidateUpdate(ctx context.Context, oldUser, newUser *v1alpha1.ProxyUser) (admission.Warnings, error) {
	return nil, validateProxyUser(newUser)
}

func (w *ProxyUserWebhook) ValidateDelete(ctx context.Context, user *v1alpha1.ProxyUser) (admission.Warnings, error) {
	return nil, nil
}

var validProtocols = map[string]bool{
	"vless":  true,
	"trojan": true,
	"socks5": true,
	"http":   true,
}

func validateProxyUser(user *v1alpha1.ProxyUser) error {
	var allErrs field.ErrorList

	if user.Spec.Protocol == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "protocol"), "protocol must not be empty"))
	} else if !validProtocols[user.Spec.Protocol] {
		allErrs = append(allErrs, field.NotSupported(
			field.NewPath("spec", "protocol"),
			user.Spec.Protocol,
			[]string{"vless", "trojan", "socks5", "http"},
		))
	}

	if user.Spec.AuthSecret.Name == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "authSecret", "name"), "authSecret.name must not be empty"))
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

// SetupProxyUserWebhookWithManager registers the webhook with the manager.
func SetupProxyUserWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.ProxyUser{}).
		WithValidator(&ProxyUserWebhook{}).
		Complete()
}
