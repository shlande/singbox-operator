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

var _ = Describe("CustomRoute Reconciler", func() {
	ctx := context.Background()
	timeout := 10 * time.Second
	interval := 100 * time.Millisecond

	var (
		reconciler *CustomRouteReconciler
		ns         string
	)

	BeforeEach(func() {
		reconciler = &CustomRouteReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		ns = fmt.Sprintf("pr-test-%d", GinkgoParallelProcess())
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	It("should resolve inbound and outbound nodes and update status", func() {
		inboundName := "pr-inbound-1"
		outboundName := "pr-outbound-1"

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
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

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-2",
				Address: "20.0.0.2",
				Region:  "pr-other-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
			},
		}
		Expect(k8sClient.Create(ctx, outboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, outboundNode) })

		routeName := "pr-route-1"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
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

		updatedRoute := &proxyv1alpha1.CustomRoute{}
		Eventually(func() string {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			return updatedRoute.Status.ResolvedInboundNode
		}, timeout, interval).Should(Equal(inboundName))
		Expect(updatedRoute.Status.ResolvedOutboundNode).To(Equal(outboundName))
	})

	It("should set Degraded when inboundNode does not exist", func() {
		routeName := "pr-degraded-inbound"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
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

		updatedRoute := &proxyv1alpha1.CustomRoute{}
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
		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
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
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
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

		updatedRoute := &proxyv1alpha1.CustomRoute{}
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

	It("should handle reconcile of deleted CustomRoute gracefully", func() {
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: "nonexistent-route", Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should resolve route with explicit SingBoxNode outboundKind", func() {
		inboundName := "pr-kind-inbound-1"
		outboundName := "pr-kind-outbound-1"

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-4",
				Address: "20.0.0.4",
				Region:  "pr-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30450},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-5",
				Address: "20.0.0.5",
				Region:  "pr-other-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
			},
		}
		Expect(k8sClient.Create(ctx, outboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, outboundNode) })

		routeName := "pr-route-kind-singboxnode"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
				OutboundKind: proxyv1alpha1.OutboundKindSingBoxNode,
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.CustomRoute{}
		Eventually(func() string {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			return updatedRoute.Status.ResolvedOutboundNode
		}, timeout, interval).Should(Equal(outboundName))
		Expect(updatedRoute.Status.ResolvedInboundNode).To(Equal(inboundName))
	})

	It("should resolve route with ExternalOutbound outboundKind when target is Accepted", func() {
		inboundName := "pr-ext-inbound-1"
		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-6",
				Address: "20.0.0.6",
				Region:  "pr-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30451},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		extName := "pr-ext-target-1"
		externalOutbound := &proxyv1alpha1.ExternalOutbound{
			ObjectMeta: metav1.ObjectMeta{Name: extName, Namespace: ns},
			Spec: proxyv1alpha1.ExternalOutboundSpec{
				Protocol: proxyv1alpha1.ExternalProtocolTrojan,
				Server:   "203.0.113.10",
				Port:     443,
				TLS:      &proxyv1alpha1.ExternalOutboundTLS{ServerName: "example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, externalOutbound)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, externalOutbound) })

		externalOutbound.Status.Conditions = []metav1.Condition{{
			Type:               proxyv1alpha1.ExternalOutboundAcceptedConditionType,
			Status:             metav1.ConditionTrue,
			Reason:             "Validated",
			Message:            "spec is valid",
			ObservedGeneration: externalOutbound.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, externalOutbound)).To(Succeed())

		routeName := "pr-route-ext-1"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: extName,
				OutboundKind: proxyv1alpha1.OutboundKindExternalOutbound,
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.CustomRoute{}
		Eventually(func() string {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			return updatedRoute.Status.ResolvedOutboundNode
		}, timeout, interval).Should(Equal(extName))
		Expect(updatedRoute.Status.ResolvedInboundNode).To(Equal(inboundName))

		var ready *metav1.Condition
		for i, c := range updatedRoute.Status.Conditions {
			if c.Type == "Ready" {
				ready = &updatedRoute.Status.Conditions[i]
			}
		}
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should set Degraded when ExternalOutbound target does not exist", func() {
		inboundName := "pr-ext-inbound-2"
		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-7",
				Address: "20.0.0.7",
				Region:  "pr-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30452},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		routeName := "pr-route-ext-missing"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: "nonexistent-external",
				OutboundKind: proxyv1alpha1.OutboundKindExternalOutbound,
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.CustomRoute{}
		Eventually(func() bool {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			for _, c := range updatedRoute.Status.Conditions {
				if c.Type == "Degraded" && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		var degradedReason, degradedMsg string
		for _, c := range updatedRoute.Status.Conditions {
			if c.Type == "Degraded" {
				degradedReason = c.Reason
				degradedMsg = c.Message
			}
		}
		Expect(degradedReason).To(Equal("OutboundNodeNotFound"))
		Expect(degradedMsg).To(ContainSubstring("outboundNode"))
	})

	It("should set Degraded when ExternalOutbound target is not Accepted", func() {
		inboundName := "pr-ext-inbound-3"
		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: ns},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-pr-8",
				Address: "20.0.0.8",
				Region:  "pr-test-region",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30453},
				},
			},
		}
		Expect(k8sClient.Create(ctx, inboundNode)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, inboundNode) })

		extName := "pr-ext-target-2"
		externalOutbound := &proxyv1alpha1.ExternalOutbound{
			ObjectMeta: metav1.ObjectMeta{Name: extName, Namespace: ns},
			Spec: proxyv1alpha1.ExternalOutboundSpec{
				Protocol: proxyv1alpha1.ExternalProtocolTrojan,
				Server:   "203.0.113.11",
				Port:     443,
				TLS:      &proxyv1alpha1.ExternalOutboundTLS{ServerName: "example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, externalOutbound)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, externalOutbound) })

		externalOutbound.Status.Conditions = []metav1.Condition{{
			Type:               proxyv1alpha1.ExternalOutboundAcceptedConditionType,
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            "credentials secret missing",
			ObservedGeneration: externalOutbound.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, externalOutbound)).To(Succeed())

		routeName := "pr-route-ext-rejected"
		route := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: ns},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: extName,
				OutboundKind: proxyv1alpha1.OutboundKindExternalOutbound,
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { k8sClient.Delete(ctx, route) })

		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: routeName, Namespace: ns},
		})
		Expect(err).NotTo(HaveOccurred())

		updatedRoute := &proxyv1alpha1.CustomRoute{}
		Eventually(func() bool {
			k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ns}, updatedRoute)
			for _, c := range updatedRoute.Status.Conditions {
				if c.Type == "Degraded" && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		var degradedReason, degradedMsg string
		for _, c := range updatedRoute.Status.Conditions {
			if c.Type == "Degraded" {
				degradedReason = c.Reason
				degradedMsg = c.Message
			}
		}
		Expect(degradedReason).To(Equal("OutboundNotAccepted"))
		Expect(degradedMsg).To(ContainSubstring("credentials secret missing"))
		Expect(updatedRoute.Status.ResolvedOutboundNode).To(BeEmpty())
	})
})
