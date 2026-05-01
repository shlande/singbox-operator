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

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

var _ = Describe("ProxyRoute Reconciler", func() {
	ctx := context.Background()
	timeout := 10 * time.Second
	interval := 100 * time.Millisecond

	var (
		reconciler *ProxyRouteReconciler
		ns         string
	)

	BeforeEach(func() {
		reconciler = &ProxyRouteReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		ns = fmt.Sprintf("pr-test-%d", GinkgoParallelProcess())
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	It("should resolve inbound and outbound nodes and update status", func() {
		inboundName := "pr-inbound-1"
		outboundName := "pr-outbound-1"

		inboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-pr-1",
				Address: "20.0.0.1",
				Region:  "pr-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30448},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		outboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef:       "k8s-node-pr-2",
				Address:       "20.0.0.2",
				Region:        "pr-other-region",
				Roles:         []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
			},
		}
		Expect(k8sClient.Create(ctx, outboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, outboundNode) })

		routeName := "pr-route-1"
		route := &proxyv1alpha1.ProxyRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.ProxyRoute{}
		Eventually(func() string {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			return updatedRoute.Status.ResolvedInboundNode
		}, timeout, interval).Should(Equal(inboundName))
		Expect(updatedRoute.Status.ResolvedOutboundNode).To(Equal(outboundName))
	})

	It("should set Degraded when inboundNode does not exist", func() {
		routeName := "pr-degraded-inbound"
		route := &proxyv1alpha1.ProxyRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyRouteSpec{
				InboundNode:  "nonexistent-inbound",
				OutboundNode: "nonexistent-outbound",
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.ProxyRoute{}
		Eventually(func() bool {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			for _, c := range updatedRoute.Status.Conditions {
				if c.Type == "Degraded" && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		var degradedMsg string
		for _, c := range updatedRoute.Status.Conditions {
			if c.Type == "Degraded" {
				degradedMsg = c.Message
			}
		}
		Expect(degradedMsg).To(ContainSubstring("inboundNode"))
	})

	It("should set Degraded when outboundNode does not exist", func() {
		inboundName := "pr-inbound-only"
		inboundNode := &proxyv1alpha1.ProxyNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyNodeSpec{
				NodeRef: "k8s-node-pr-3",
				Address: "20.0.0.3",
				Region:  "pr-degraded-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30449},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		routeName := "pr-degraded-outbound"
		route := &proxyv1alpha1.ProxyRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.ProxyRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: "nonexistent-outbound",
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.ProxyRoute{}
		Eventually(func() bool {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			for _, c := range updatedRoute.Status.Conditions {
				if c.Type == "Degraded" && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		var degradedMsg string
		for _, c := range updatedRoute.Status.Conditions {
			if c.Type == "Degraded" {
				degradedMsg = c.Message
			}
		}
		Expect(degradedMsg).To(ContainSubstring("outboundNode"))
	})

	It("should handle reconcile of deleted ProxyRoute gracefully", func() {
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "nonexistent-route", Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
