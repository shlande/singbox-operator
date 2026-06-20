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

// UserGroupCustomValidator handles validation for UserGroup.
type UserGroupCustomValidator struct{}

func (v *UserGroupCustomValidator) ValidateCreate(ctx context.Context, obj *v1alpha1.UserGroup) (admission.Warnings, error) {
	return nil, validateUserGroup(obj)
}

func (v *UserGroupCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *v1alpha1.UserGroup) (admission.Warnings, error) {
	return nil, validateUserGroup(newObj)
}

func (v *UserGroupCustomValidator) ValidateDelete(ctx context.Context, obj *v1alpha1.UserGroup) (admission.Warnings, error) {
	return nil, nil
}

func validateUserGroup(ug *v1alpha1.UserGroup) error {
	var allErrs field.ErrorList

	for i, name := range ug.Spec.AllowedNodes {
		if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "allowedNodes").Index(i),
				name,
				"must be a valid DNS subdomain: "+errs[0],
			))
		}
	}

	for i, name := range ug.Spec.DeniedNodes {
		if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "deniedNodes").Index(i),
				name,
				"must be a valid DNS subdomain: "+errs[0],
			))
		}
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}

func SetupUserGroupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.UserGroup{}).
		WithValidator(&UserGroupCustomValidator{}).
		Complete()
}