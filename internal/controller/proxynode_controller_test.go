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
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

var _ = Describe("ProxyNode Reconciler", func() {
	const (
		testTimeout  = 10 * time.Second
		testInterval = 100 * time.Millisecond
	)

	var (
		testCtx    context.Context
		reconciler *ProxyNodeReconciler
	)

	BeforeEach(func() {
		testCtx = context.Background()
		reconciler = &ProxyNodeReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	It("should create ConfigMap, Deployment, and Services for inbound node", func() {
		nodeName := "test-inbound-1"
		node := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, node)
		})

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data).To(HaveKey("config.json"))

		deploy := &appsv1.Deployment{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-deploy", Namespace: "default"}, deploy)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(deploy.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("kubernetes.io/hostname", "k8s-node-1"))

		relaySvc := &corev1.Service{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-relay-svc", Namespace: "default"}, relaySvc)
		}, testTimeout, testInterval).Should(Succeed())

		entrySvc := &corev1.Service{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-vless-entry-svc", Namespace: "default"}, entrySvc)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(entrySvc.Spec.Ports[0].Port).To(Equal(int32(10443)))
	})

	It("should create ConfigMap with socks5 relay inbound for outbound node", func() {
		nodeName := "test-outbound-1"
		node := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef:       "k8s-node-2",
				Address:       "2.3.4.5",
				Region:        "us-west",
				Roles:         []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, node)
		})

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())

		var config map[string]interface{}
		Expect(json.Unmarshal([]byte(cm.Data["config.json"]), &config)).To(Succeed())
		inbounds, ok := config["inbounds"].([]interface{})
		Expect(ok).To(BeTrue())
		Expect(inbounds).To(HaveLen(1))
		Expect(inbounds[0].(map[string]interface{})["type"]).To(Equal("socks"))

		relaySvc := &corev1.Service{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-relay-svc", Namespace: "default"}, relaySvc)
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("should include outbound node address in inbound ConfigMap when in same region", func() {
		outboundName := "test-outbound-cascade"
		inboundName := "test-inbound-cascade"

		outboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef:       "k8s-node-3",
				Address:       "3.4.5.6",
				Region:        "eu-west",
				Roles:         []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, outboundNode)
		})

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		inboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-4",
				Address: "4.5.6.7",
				Region:  "eu-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, inboundNode)
		})

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("3.4.5.6"))
	})

	It("should update inbound ConfigMap when outbound node address changes", func() {
		outboundName := "test-outbound-addr"
		inboundName := "test-inbound-addr"

		outboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef:       "k8s-node-5",
				Address:       "5.6.7.8",
				Region:        "ap-east",
				Roles:         []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, outboundNode)
		})

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		inboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-6",
				Address: "6.7.8.9",
				Region:  "ap-east",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(testCtx, inboundNode)
		})

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)).To(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("5.6.7.8"))

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: outboundName, Namespace: "default"}, outboundNode)).To(Succeed())
		outboundNode.Spec.Address = "9.9.9.9"
		Expect(k8sClient.Update(testCtx, outboundNode)).To(Succeed())

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)).To(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("9.9.9.9"))
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("5.6.7.8"))
	})

	It("should remove finalizer when ProxyNode is deleted", func() {
		nodeName := "test-delete-node"
		node := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-7",
				Address: "7.8.9.0",
				Region:  "sa-east",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
				RelayPort:     10808,
				RelayProtocol: "socks5",
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)).To(Succeed())

		Expect(k8sClient.Delete(testCtx, node)).To(Succeed())

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		deletedNode := &proxyv1alpha1.ProxyNode{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName, Namespace: "default"}, deletedNode)
		if err == nil {
			Expect(deletedNode.Finalizers).NotTo(ContainElement(proxyNodeFinalizer))
		}
	})
})
