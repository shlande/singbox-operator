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

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

// UserGroupReconciler reconciles a UserGroup object
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=usergroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=singboxoperator.shlande.top,resources=users,verbs=get;list;watch;patch
type UserGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *UserGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	userGroup := &proxyv1alpha1.UserGroup{}
	if err := r.Get(ctx, req.NamespacedName, userGroup); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// UserGroup deleted — still trigger any Users that referenced it
		logger.Info("UserGroup not found, triggering referencing Users", "name", req.Name)
	}

	userList := &proxyv1alpha1.UserList{}
	if err := r.List(ctx, userList,
		client.InNamespace(req.Namespace),
		client.MatchingFields{"spec.userGroupRef": req.Name},
	); err != nil {
		return ctrl.Result{}, err
	}

	timestamp := time.Now().Format(time.RFC3339)
	for i := range userList.Items {
		user := &userList.Items[i]
		patch := client.MergeFrom(user.DeepCopy())
		if user.Annotations == nil {
			user.Annotations = make(map[string]string)
		}
		user.Annotations["singboxoperator.shlande.top/reconcile-trigger"] = timestamp
		if err := r.Patch(ctx, user, patch); err != nil {
			logger.Error(err, "Failed to patch User reconcile trigger", "user", user.Name)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.UserGroup{}).
		Named("usergroup").
		Complete(r)
}
