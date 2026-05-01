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

package helpers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

func CreateProxyNode(ctx context.Context, c client.Client, name, namespace, region, address string, roles []v1alpha1.ProxyRole, protocols []v1alpha1.ProtocolConfig) (*v1alpha1.ProxyNode, error) {
	node := &v1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ProxyNodeSpec{
			NodeRef:            name + "-k8s-node",
			Address:            address,
			Region:             region,
			Roles:              roles,
			SupportedProtocols: protocols,
		},
	}
	if err := c.Create(ctx, node); err != nil {
		return nil, fmt.Errorf("creating ProxyNode %s: %w", name, err)
	}
	return node, nil
}

func CreateProxyUser(ctx context.Context, c client.Client, name, namespace, protocol, secretName string) (*v1alpha1.ProxyUser, error) {
	user := &v1alpha1.ProxyUser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ProxyUserSpec{
			Protocol: protocol,
			AuthSecret: corev1.SecretReference{
				Name:      secretName,
				Namespace: namespace,
			},
		},
	}
	if err := c.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("creating ProxyUser %s: %w", name, err)
	}
	return user, nil
}

func CreateAuthSecret(ctx context.Context, c client.Client, name, namespace, uuid, password string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"uuid":     []byte(uuid),
			"password": []byte(password),
		},
	}
	return c.Create(ctx, secret)
}

func WaitForCondition(ctx context.Context, timeout time.Duration, condition func() (bool, error)) error {
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, timeout, true, func(ctx context.Context) (bool, error) {
		return condition()
	})
}

func GetConfigMapData(ctx context.Context, c client.Client, name, namespace string) (map[string]string, error) {
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm); err != nil {
		return nil, err
	}
	return cm.Data, nil
}
