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

package credmanager

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/shlande/singbox-operator/api/v1alpha1"
)

// NodeCredential holds SOCKS5 credentials for inter-node relay.
type NodeCredential struct {
	Username string
	Password string
}

// UserCredential holds the UUID that is the single source of truth for all
// per-protocol credential derivation. Concrete auth values (passwords, UUIDs
// per outbound node) are derived at config-generation time via configengine.
type UserCredential struct {
	UUID string
}

// secretName returns the Secret name for a ProxyNode's relay credentials.
func secretName(nodeName string) string {
	return fmt.Sprintf("proxynode-%s-relay-cred", nodeName)
}

// EnsureNodeCredential creates or retrieves the relay credential Secret for a ProxyNode.
// If the Secret doesn't exist, it generates random credentials and creates it.
// Sets OwnerReference to the ProxyNode for automatic GC on deletion.
func EnsureNodeCredential(ctx context.Context, c client.Client, node *v1alpha1.ProxyNode) (NodeCredential, error) {
	name := secretName(node.Name)
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: node.Namespace}, secret)
	if err == nil {
		return NodeCredential{
			Username: string(secret.Data["username"]),
			Password: string(secret.Data["password"]),
		}, nil
	}
	if !errors.IsNotFound(err) {
		return NodeCredential{}, fmt.Errorf("getting relay credential secret: %w", err)
	}

	username := generateUUID()
	password, err := generatePassword()
	if err != nil {
		return NodeCredential{}, fmt.Errorf("generating password: %w", err)
	}

	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: node.Namespace,
		},
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
	}

	if err := controllerutil.SetControllerReference(node, secret, c.Scheme()); err != nil {
		return NodeCredential{}, fmt.Errorf("setting owner reference: %w", err)
	}

	if err := c.Create(ctx, secret); err != nil {
		return NodeCredential{}, fmt.Errorf("creating relay credential secret: %w", err)
	}

	return NodeCredential{Username: username, Password: password}, nil
}

// GetNodeCredential retrieves the relay credential for a ProxyNode by name.
func GetNodeCredential(ctx context.Context, c client.Client, nodeName, namespace string) (NodeCredential, error) {
	name := secretName(nodeName)
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return NodeCredential{}, fmt.Errorf("getting relay credential secret for node %s: %w", nodeName, err)
	}
	return NodeCredential{
		Username: string(secret.Data["username"]),
		Password: string(secret.Data["password"]),
	}, nil
}

// GetUserCredential retrieves authentication credentials from the Secret referenced by a ProxyUser.
func GetUserCredential(ctx context.Context, c client.Client, user *v1alpha1.ProxyUser) (UserCredential, error) {
	ref := user.Spec.AuthSecret
	ns := ref.Namespace
	if ns == "" {
		ns = user.Namespace
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: ns}, secret); err != nil {
		return UserCredential{}, fmt.Errorf("getting auth secret for user %s: %w", user.Name, err)
	}
	return UserCredential{
		UUID: string(secret.Data["uuid"]),
	}, nil
}

// generateUUID generates a random UUID v4 string.
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// generatePassword generates a random 32-byte base64-encoded password.
func generatePassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
