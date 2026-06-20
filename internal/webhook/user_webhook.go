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

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// UserWebhook handles defaulting and validation for User.
type UserWebhook struct{}

func (w *UserWebhook) Default(ctx context.Context, user *v1alpha1.User) error {
	if user.Spec.Protocol == "" {
		user.Spec.Protocol = "hysteria2"
	}
	return nil
}

func (w *UserWebhook) ValidateCreate(ctx context.Context, user *v1alpha1.User) (admission.Warnings, error) {
	return nil, validateUser(user)
}

func (w *UserWebhook) ValidateUpdate(ctx context.Context, oldUser, newUser *v1alpha1.User) (admission.Warnings, error) {
	return nil, validateUser(newUser)
}

func (w *UserWebhook) ValidateDelete(ctx context.Context, user *v1alpha1.User) (admission.Warnings, error) {
	return nil, nil
}

var validProtocols = map[string]bool{
	"hysteria2": true,
	"vless":     true,
	"trojan":    true,
	"socks5":    true,
	"http":      true,
}

func validateUser(user *v1alpha1.User) error {
	var allErrs field.ErrorList

	if user.Spec.Protocol != "" && !validProtocols[user.Spec.Protocol] {
		allErrs = append(allErrs, field.NotSupported(
			field.NewPath("spec", "protocol"),
			user.Spec.Protocol,
			[]string{"hysteria2", "vless", "trojan", "socks5", "http"},
		))
	}

	if user.Spec.AuthSecret.Name == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "authSecret", "name"), "authSecret.name must not be empty"))
	}

	if user.Spec.UserGroupRef != "" {
		if errs := validation.IsDNS1123Subdomain(user.Spec.UserGroupRef); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "userGroupRef"),
				user.Spec.UserGroupRef,
				"must be a valid DNS subdomain: "+errs[0],
			))
		}
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

func SetupUserWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.User{}).
		WithDefaulter(&UserWebhook{}).
		WithValidator(&UserWebhook{}).
		Complete()
}
