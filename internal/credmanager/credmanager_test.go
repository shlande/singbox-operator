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

package credmanager_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/your-org/singbox-operator/api/v1alpha1"
	"github.com/your-org/singbox-operator/internal/credmanager"
)

func setupEnvtest(t *testing.T) (client.Client, func()) {
	t.Helper()

	crdPath := filepath.Join("..", "..", "config", "crd", "bases")

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}

	if binDir := getFirstFoundEnvTestBinaryDir(); binDir != "" {
		testEnv.BinaryAssetsDirectory = binDir
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}

	if err := v1alpha1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("creating client: %v", err)
	}

	return c, func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stopping envtest: %v", err)
		}
	}
}

func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

func TestEnsureNodeCredential_Idempotent(t *testing.T) {
	c, cleanup := setupEnvtest(t)
	defer cleanup()
	ctx := context.Background()

	node := &v1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-node",
			Namespace: "default",
		},
		Spec: v1alpha1.ProxyNodeSpec{
			NodeRef:   "k8s-node-1",
			Address:   "1.2.3.4",
			Region:    "us-west",
			Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
			RelayPort: 10808,
		},
	}
	if err := c.Create(ctx, node); err != nil {
		t.Fatalf("creating ProxyNode: %v", err)
	}

	cred1, err := credmanager.EnsureNodeCredential(ctx, c, node)
	if err != nil {
		t.Fatalf("first EnsureNodeCredential: %v", err)
	}
	if cred1.Username == "" || cred1.Password == "" {
		t.Error("expected non-empty credentials")
	}

	cred2, err := credmanager.EnsureNodeCredential(ctx, c, node)
	if err != nil {
		t.Fatalf("second EnsureNodeCredential: %v", err)
	}
	if cred1.Username != cred2.Username || cred1.Password != cred2.Password {
		t.Errorf("credentials not idempotent: %+v != %+v", cred1, cred2)
	}
}

func TestGetNodeCredential(t *testing.T) {
	c, cleanup := setupEnvtest(t)
	defer cleanup()
	ctx := context.Background()

	node := &v1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-node-2",
			Namespace: "default",
		},
		Spec: v1alpha1.ProxyNodeSpec{
			NodeRef:   "k8s-node-2",
			Address:   "2.3.4.5",
			Region:    "us-east",
			Roles:     []v1alpha1.ProxyRole{v1alpha1.ProxyRoleOutbound},
			RelayPort: 10808,
		},
	}
	if err := c.Create(ctx, node); err != nil {
		t.Fatalf("creating ProxyNode: %v", err)
	}

	created, err := credmanager.EnsureNodeCredential(ctx, c, node)
	if err != nil {
		t.Fatalf("EnsureNodeCredential: %v", err)
	}

	retrieved, err := credmanager.GetNodeCredential(ctx, c, node.Name, node.Namespace)
	if err != nil {
		t.Fatalf("GetNodeCredential: %v", err)
	}
	if created.Username != retrieved.Username || created.Password != retrieved.Password {
		t.Errorf("retrieved credentials don't match: %+v != %+v", created, retrieved)
	}
}

func TestGetUserCredential(t *testing.T) {
	c, cleanup := setupEnvtest(t)
	defer cleanup()
	ctx := context.Background()

	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-a-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"uuid": []byte("550e8400-e29b-41d4-a716-446655440000"),
		},
	}
	if err := c.Create(ctx, authSecret); err != nil {
		t.Fatalf("creating auth secret: %v", err)
	}

	user := &v1alpha1.ProxyUser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-a",
			Namespace: "default",
		},
		Spec: v1alpha1.ProxyUserSpec{
			Protocol: "vless",
			AuthSecret: corev1.SecretReference{
				Name:      "user-a-secret",
				Namespace: "default",
			},
		},
	}
	if err := c.Create(ctx, user); err != nil {
		t.Fatalf("creating ProxyUser: %v", err)
	}

	cred, err := credmanager.GetUserCredential(ctx, c, user)
	if err != nil {
		t.Fatalf("GetUserCredential: %v", err)
	}
	if cred.UUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected UUID, got: %s", cred.UUID)
	}
}
