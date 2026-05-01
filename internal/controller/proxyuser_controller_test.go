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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	proxyv1alpha1 "github.com/your-org/singbox-operator/api/v1alpha1"
)

var _ = Describe("ProxyUser Reconciler", func() {
	const (
		ns       = "default"
		timeout  = 10 * time.Second
		interval = 100 * time.Millisecond
	)

	var (
		testCtx    context.Context
		reconciler *ProxyUserReconciler
	)

	BeforeEach(func() {
		testCtx = context.Background()
		reconciler = &ProxyUserReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	It("should update status with matching inbound nodes", func() {
		nodeName := "pu-test-inbound-1"
		node := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-pu-1",
				Address: "10.0.0.1",
				Region:  "pu-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, node) })

		authSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "pu-test-secret-1", Namespace: ns},
			Data:       map[string][]byte{"uuid": []byte("test-uuid-1")},
		}
		Expect(k8sClient.Create(testCtx, authSecret)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, authSecret) })

		userName := "pu-test-user-1"
		user := &proxyv1alpha1.ProxyUser{
			ObjectMeta: metav1.ObjectMeta{Name: userName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyUserSpec{
				Protocol: "vless",
				AuthSecret: corev1.SecretReference{
					Name:      "pu-test-secret-1",
					Namespace: ns,
				},
			},
		}
		Expect(k8sClient.Create(testCtx, user)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, user) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: userName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedUser := &proxyv1alpha1.ProxyUser{}
		Eventually(func() bool {
			k8sClient.Get(testCtx, types.NamespacedName{Name: userName, Namespace: ns}, updatedUser)
			return updatedUser.Status.ActiveNodeCount >= 1
		}, timeout, interval).Should(BeTrue())
		Expect(updatedUser.Status.ActiveNodes).To(ContainElement(nodeName))
	})

	It("should count multiple matching inbound nodes", func() {
		for i := 1; i <= 2; i++ {
			nodeName := fmt.Sprintf("pu-multi-node-%d", i)
			node := &proxyv1alpha1.ProxyNode{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: ns},
				Spec: proxyv1alpha1.ProxyNodeSpec{
					NodeRef: fmt.Sprintf("k8s-node-pu-multi-%d", i),
					Address: fmt.Sprintf("10.1.0.%d", i),
					Region:  "pu-multi-region",
					Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
					SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
						{Protocol: "vless", Port: 10443},
					},
					RelayPort:     10808,
					RelayProtocol: "socks5",
				},
			}
			Expect(k8sClient.Create(testCtx, node)).To(Succeed())
			DeferCleanup(func() { k8sClient.Delete(testCtx, node) })
		}

		authSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "pu-multi-secret", Namespace: ns},
			Data:       map[string][]byte{"uuid": []byte("test-uuid-multi")},
		}
		Expect(k8sClient.Create(testCtx, authSecret)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, authSecret) })

		userName := "pu-multi-user"
		user := &proxyv1alpha1.ProxyUser{
			ObjectMeta: metav1.ObjectMeta{Name: userName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyUserSpec{
				Protocol:   "vless",
				AuthSecret: corev1.SecretReference{Name: "pu-multi-secret", Namespace: ns},
			},
		}
		Expect(k8sClient.Create(testCtx, user)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, user) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: userName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedUser := &proxyv1alpha1.ProxyUser{}
		Eventually(func() int32 {
			k8sClient.Get(testCtx, types.NamespacedName{Name: userName, Namespace: ns}, updatedUser)
			return updatedUser.Status.ActiveNodeCount
		}, timeout, interval).Should(BeNumerically(">=", int32(2)))
	})

	It("should set ActiveNodeCount=0 when no nodes support the protocol", func() {
		authSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "pu-nomatch-secret", Namespace: ns},
			Data:       map[string][]byte{"password": []byte("test-password")},
		}
		Expect(k8sClient.Create(testCtx, authSecret)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, authSecret) })

		userName := "pu-nomatch-user"
		user := &proxyv1alpha1.ProxyUser{
			ObjectMeta: metav1.ObjectMeta{Name: userName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyUserSpec{
				Protocol:   "trojan",
				AuthSecret: corev1.SecretReference{Name: "pu-nomatch-secret", Namespace: ns},
			},
		}
		Expect(k8sClient.Create(testCtx, user)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(testCtx, user) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: userName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedUser := &proxyv1alpha1.ProxyUser{}
		Eventually(func() bool {
			k8sClient.Get(testCtx, types.NamespacedName{Name: userName, Namespace: ns}, updatedUser)
			return updatedUser.Status.ObservedGeneration > 0
		}, timeout, interval).Should(BeTrue())
		Expect(updatedUser.Status.ActiveNodeCount).To(Equal(int32(0)))
	})

	It("should handle reconcile of deleted ProxyUser gracefully", func() {
		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "nonexistent-user", Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
