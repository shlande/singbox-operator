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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

var _ = Describe("NodeReadiness", func() {
	const (
		testTimeout  = 10 * time.Second
		testInterval = 100 * time.Millisecond
	)

	var (
		testCtx    context.Context
		reconciler *SingBoxNodeReconciler
	)

	BeforeEach(func() {
		testCtx = context.Background()
		reconciler = &SingBoxNodeReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	makeSBN := func(name, nodeRef string) *proxyv1alpha1.SingBoxNode {
		return &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: nodeRef,
				Address: "1.2.3.4",
				Region:  "us",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 10443},
				},
			},
		}
	}

	makeK8sNode := func(name string, ready bool) *corev1.Node {
		status := corev1.ConditionTrue
		if !ready {
			status = corev1.ConditionFalse
		}
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: status},
				},
			},
		}
	}

	nodeReadyConditionTrue := func(sbnName string) func() bool {
		return func() bool {
			sbn := &proxyv1alpha1.SingBoxNode{}
			if err := k8sClient.Get(testCtx, types.NamespacedName{Name: sbnName, Namespace: "default"}, sbn); err != nil {
				return false
			}
			for _, c := range sbn.Status.Conditions {
				if c.Type == proxyv1alpha1.NodeReadyConditionType {
					return c.Status == metav1.ConditionTrue
				}
			}
			return false
		}
	}

	nodeReadyConditionFalse := func(sbnName string) func() bool {
		return func() bool {
			sbn := &proxyv1alpha1.SingBoxNode{}
			if err := k8sClient.Get(testCtx, types.NamespacedName{Name: sbnName, Namespace: "default"}, sbn); err != nil {
				return false
			}
			for _, c := range sbn.Status.Conditions {
				if c.Type == proxyv1alpha1.NodeReadyConditionType {
					return c.Status == metav1.ConditionFalse
				}
			}
			return false
		}
	}

	nodeReadyConditionReason := func(sbnName, reason string) func() bool {
		return func() bool {
			sbn := &proxyv1alpha1.SingBoxNode{}
			if err := k8sClient.Get(testCtx, types.NamespacedName{Name: sbnName, Namespace: "default"}, sbn); err != nil {
				return false
			}
			for _, c := range sbn.Status.Conditions {
				if c.Type == proxyv1alpha1.NodeReadyConditionType {
					return c.Status == metav1.ConditionFalse && c.Reason == reason
				}
			}
			return false
		}
	}

	reconcile := func(name string) {
		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		// Second reconcile for finalizer / subresource consistency
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	It("TestNodeReadiness_SetsNodeReadyTrue_WhenNodeIsReady", func() {
		k8sNode := makeK8sNode("worker-ready", true)
		Expect(k8sClient.Create(testCtx, k8sNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, k8sNode) })
		Expect(k8sClient.Status().Update(testCtx, k8sNode)).To(Succeed())

		sbn := makeSBN("sbn-ready", "worker-ready")
		Expect(k8sClient.Create(testCtx, sbn)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, sbn) })

		reconcile("sbn-ready")

		Eventually(nodeReadyConditionTrue("sbn-ready"), testTimeout, testInterval).Should(BeTrue())
	})

	It("TestNodeReadiness_SetsNodeReadyFalse_WhenNodeIsNotReady", func() {
		k8sNode := makeK8sNode("worker-notready", false)
		Expect(k8sClient.Create(testCtx, k8sNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, k8sNode) })
		Expect(k8sClient.Status().Update(testCtx, k8sNode)).To(Succeed())

		sbn := makeSBN("sbn-notready", "worker-notready")
		Expect(k8sClient.Create(testCtx, sbn)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, sbn) })

		reconcile("sbn-notready")

		Eventually(nodeReadyConditionFalse("sbn-notready"), testTimeout, testInterval).Should(BeTrue())
	})

	It("TestNodeReadiness_SetsNodeReadyFalse_WhenNodeNotFound", func() {
		sbn := makeSBN("sbn-no-node", "nonexistent-node")
		Expect(k8sClient.Create(testCtx, sbn)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, sbn) })

		reconcile("sbn-no-node")

		Eventually(nodeReadyConditionReason("sbn-no-node", "NodeNotFound"), testTimeout, testInterval).Should(BeTrue())
	})

	It("TestNodeReadiness_TransitionsToFalse_WhenNodeBecomesNotReady", func() {
		k8sNode := makeK8sNode("worker-transition", true)
		Expect(k8sClient.Create(testCtx, k8sNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, k8sNode) })
		Expect(k8sClient.Status().Update(testCtx, k8sNode)).To(Succeed())

		sbn := makeSBN("sbn-transition", "worker-transition")
		Expect(k8sClient.Create(testCtx, sbn)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, sbn) })

		reconcile("sbn-transition")
		Eventually(nodeReadyConditionTrue("sbn-transition"), testTimeout, testInterval).Should(BeTrue())

		// Make the K8s Node not ready
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: "worker-transition"}, k8sNode)).To(Succeed())
		k8sNode.Status.Conditions = []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
		}
		Expect(k8sClient.Status().Update(testCtx, k8sNode)).To(Succeed())

		reconcile("sbn-transition")
		Eventually(nodeReadyConditionFalse("sbn-transition"), testTimeout, testInterval).Should(BeTrue())
	})

	It("TestNodeReadiness_Idempotent_MultipleReconciles", func() {
		k8sNode := makeK8sNode("worker-idempotent", true)
		Expect(k8sClient.Create(testCtx, k8sNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, k8sNode) })
		Expect(k8sClient.Status().Update(testCtx, k8sNode)).To(Succeed())

		sbn := makeSBN("sbn-idempotent", "worker-idempotent")
		Expect(k8sClient.Create(testCtx, sbn)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, sbn) })

		reconcile("sbn-idempotent")
		Eventually(nodeReadyConditionTrue("sbn-idempotent"), testTimeout, testInterval).Should(BeTrue())

		// Reconcile again — condition must stay True
		reconcile("sbn-idempotent")
		Eventually(nodeReadyConditionTrue("sbn-idempotent"), testTimeout, testInterval).Should(BeTrue())
	})
})

var _ = Describe("SingBoxNode Reconciler", func() {
	const (
		testTimeout  = 10 * time.Second
		testInterval = 100 * time.Millisecond
	)

	var (
		testCtx    context.Context
		reconciler *SingBoxNodeReconciler
	)

	BeforeEach(func() {
		testCtx = context.Background()
		reconciler = &SingBoxNodeReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	It("should create ConfigMap, Pod, and Services for inbound node", func() {
		nodeName := "test-inbound-1"
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-1",
				Address: "1.2.3.4",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30443},
				},
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

		pod := &corev1.Pod{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-sing-box-server", Namespace: "default"}, pod)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("kubernetes.io/hostname", "k8s-node-1"))

		containers := pod.Spec.Containers
		Expect(containers).NotTo(BeEmpty())
		Expect(pod.Spec.HostNetwork).To(BeTrue())
		Expect(pod.Spec.DNSPolicy).To(Equal(corev1.DNSClusterFirstWithHostNet))
		for _, c := range containers {
			for _, p := range c.Ports {
				Expect(p.HostPort).To(BeZero(), "hostNetwork pods must not declare hostPort entries")
			}
		}
	})

	It("should create ConfigMap with socks5 relay inbound for outbound node", func() {
		nodeName := "test-outbound-1"
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-2",
				Address: "2.3.4.5",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
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

		var config map[string]any
		Expect(json.Unmarshal([]byte(cm.Data["config.json"]), &config)).To(Succeed())
		inbounds, ok := config["inbounds"].([]any)
		Expect(ok).To(BeTrue())
		Expect(inbounds).To(HaveLen(1))
		Expect(inbounds[0].(map[string]any)["type"]).To(Equal("socks"))

		outboundPod := &corev1.Pod{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-sing-box-server", Namespace: "default"}, outboundPod)
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("should create a hostNetwork pod without hostPort entries for outbound node", func() {
		nodeName := "test-outbound-hostnetwork"
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-hn-1",
				Address:   "10.9.0.1",
				Region:    "us-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31980,
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, node) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())

		pod := &corev1.Pod{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-sing-box-server", Namespace: "default"}, pod)
		}, testTimeout, testInterval).Should(Succeed())

		Expect(pod.Spec.HostNetwork).To(BeTrue())
		Expect(pod.Spec.DNSPolicy).To(Equal(corev1.DNSClusterFirstWithHostNet))
		Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("kubernetes.io/hostname", "k8s-node-hn-1"))

		Expect(pod.Spec.Containers).NotTo(BeEmpty())
		for _, p := range pod.Spec.Containers[0].Ports {
			Expect(p.HostPort).To(BeZero(), "hostNetwork pods must not declare hostPort entries")
		}

		var configVol *corev1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == "config" {
				configVol = &pod.Spec.Volumes[i]
			}
		}
		Expect(configVol).NotTo(BeNil())
		Expect(configVol.ConfigMap).NotTo(BeNil())
		Expect(configVol.ConfigMap.Name).To(Equal(nodeName + "-config"))

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)).To(Succeed())
		Expect(pod.Annotations[configHashAnnotation]).To(Equal(
			fmt.Sprintf("%s-%d", cm.Annotations[configHashAnnotation], podTemplateVersion)))
	})

	It("should roll a pod carrying a pre-template-version config-hash annotation exactly once", func() {
		nodeName := "test-inbound-roll"
		podKey := types.NamespacedName{Name: nodeName + "-sing-box-server", Namespace: "default"}
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-roll-1",
				Address: "10.9.1.1",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30470},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, node) })

		reconcileReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"}}
		_, err := reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())

		pod := &corev1.Pod{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, podKey, pod)
		}, testTimeout, testInterval).Should(Succeed())

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)).To(Succeed())
		oldHash := cm.Annotations[configHashAnnotation]
		composedHash := fmt.Sprintf("%s-%d", oldHash, podTemplateVersion)
		Expect(pod.Annotations[configHashAnnotation]).To(Equal(composedHash))

		// Simulate a pod surviving from the hostPort era: bare config hash,
		// no template-version suffix.
		pod.Annotations[configHashAnnotation] = oldHash
		Expect(k8sClient.Update(testCtx, pod)).To(Succeed())

		// Mismatch pass: delete the old pod and requeue — no replacement yet.
		res, err := reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(3 * time.Second))
		Expect(errors.IsNotFound(k8sClient.Get(testCtx, podKey, &corev1.Pod{}))).To(BeTrue(),
			"old pod must be deleted without a same-pass replacement")

		// Next pass creates the replacement with the composed annotation.
		_, err = reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			return k8sClient.Get(testCtx, podKey, pod)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(pod.Annotations[configHashAnnotation]).To(Equal(composedHash))
		Expect(pod.Spec.HostNetwork).To(BeTrue())
	})

	It("should keep requeueing without recreating while the old pod is stuck Terminating", func() {
		nodeName := "test-inbound-terminating"
		podKey := types.NamespacedName{Name: nodeName + "-sing-box-server", Namespace: "default"}
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-term-1",
				Address: "10.9.2.1",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30471},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, node) })

		reconcileReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: nodeName, Namespace: "default"}}
		_, err := reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())

		pod := &corev1.Pod{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, podKey, pod)
		}, testTimeout, testInterval).Should(Succeed())

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName + "-config", Namespace: "default"}, cm)).To(Succeed())
		oldHash := cm.Annotations[configHashAnnotation]

		// Old-era annotation plus a finalizer that blocks deletion (stuck Terminating).
		pod.Annotations[configHashAnnotation] = oldHash
		pod.Finalizers = append(pod.Finalizers, "test.singboxoperator.shlande.top/stuck")
		Expect(k8sClient.Update(testCtx, pod)).To(Succeed())
		oldUID := pod.UID

		// Every pass deletes idempotently and requeues without creating a replacement.
		for i := 0; i < 2; i++ {
			res, err := reconciler.Reconcile(testCtx, reconcileReq)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(3 * time.Second))

			stuck := &corev1.Pod{}
			Expect(k8sClient.Get(testCtx, podKey, stuck)).To(Succeed())
			Expect(stuck.UID).To(Equal(oldUID), "old pod must still be there while Terminating")
			Expect(stuck.DeletionTimestamp).NotTo(BeNil())
			Expect(stuck.Annotations[configHashAnnotation]).To(Equal(oldHash))
		}

		// Unblock termination; the object disappears once finalizers are cleared.
		Expect(k8sClient.Get(testCtx, podKey, pod)).To(Succeed())
		pod.Finalizers = nil
		Expect(k8sClient.Update(testCtx, pod)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(testCtx, podKey, &corev1.Pod{}))
		}, testTimeout, testInterval).Should(BeTrue())

		// Now the replacement is created with the composed annotation.
		res, err := reconciler.Reconcile(testCtx, reconcileReq)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Eventually(func() error {
			return k8sClient.Get(testCtx, podKey, pod)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(pod.Annotations[configHashAnnotation]).To(Equal(
			fmt.Sprintf("%s-%d", oldHash, podTemplateVersion)))
		Expect(pod.Spec.HostNetwork).To(BeTrue())
	})

	It("should include outbound node address in inbound ConfigMap when in same region", func() {
		outboundName := "test-outbound-cascade"
		inboundName := "test-inbound-cascade"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-3",
				Address:   "3.4.5.6",
				Region:    "eu-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31963,
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

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-4",
				Address: "4.5.6.7",
				Region:  "eu-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30445},
				},
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

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-5",
				Address:   "5.6.7.8",
				Region:    "ap-east",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31964,
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

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-6",
				Address: "6.7.8.9",
				Region:  "ap-east",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30446},
				},
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

	It("(a) should collect outbound when AllowedInbounds is empty (backward compat)", func() {
		outboundName := "test-outbound-empty-allowed"
		inboundName := "test-inbound-empty-allowed"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-empty-allowed",
				Address:   "10.0.0.1",
				Region:    "us-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31965,
				// AllowedInbounds is nil/empty → allow all (backward compat)
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-inbound-empty-allowed",
				Address: "10.0.0.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30448},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("10.0.0.1"))
	})

	It("(b) should collect outbound when AllowedInbounds includes the inbound node", func() {
		outboundName := "test-outbound-match-allowed"
		inboundName := "test-inbound-match-allowed"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:         "k8s-node-match-allowed",
				Address:         "10.0.1.1",
				Region:          "us-west",
				Roles:           []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:       31966,
				AllowedInbounds: []string{inboundName},
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-inbound-match-allowed",
				Address: "10.0.1.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30449},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// Inbound IS in the allowed list → config contains outbound IP
		Expect(cm.Data["config.json"]).To(ContainSubstring("10.0.1.1"))
	})

	It("(c) should NOT collect outbound when AllowedInbounds excludes the inbound node", func() {
		outboundName := "test-outbound-mismatch-allowed"
		inboundName := "test-inbound-mismatch-allowed"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:         "k8s-node-mismatch-allowed",
				Address:         "10.0.2.1",
				Region:          "us-west",
				Roles:           []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:       31967,
				AllowedInbounds: []string{"some-other-inbound"},
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-inbound-mismatch-allowed",
				Address: "10.0.2.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30450},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// Inbound is NOT in the allowed list → config must NOT contain outbound IP
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("10.0.2.1"))
	})

	It("(d) should skip CustomRoute when AllowedInbounds excludes the inbound node", func() {
		// Two regions: inbound in us-west, outbound in us-east (cross-region, requires CustomRoute)
		outboundName := "test-outbound-cr-mismatch"
		inboundName := "test-inbound-cr-mismatch"
		routeName := "test-route-mismatch"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:         "k8s-node-cr-outbound",
				Address:         "10.0.3.1",
				Region:          "us-east",
				Roles:           []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:       31968,
				AllowedInbounds: []string{"some-other-inbound"},
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-cr-inbound",
				Address: "10.0.3.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30451},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		customRoute := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: "default"},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
			},
		}
		Expect(k8sClient.Create(testCtx, customRoute)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, customRoute) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// CustomRoute is defined, but AllowedInbounds excludes inbound → config should NOT have outbound IP
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("10.0.3.1"))
	})

	It("(e) should reconcile inbound when outbound AllowedInbounds changes (cross-region)", func() {
		outboundName := "test-outbound-cr-change"
		inboundName := "test-inbound-cr-change"
		routeName := "test-route-change"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-cr-change-out",
				Address:   "10.0.4.1",
				Region:    "us-east",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31969,
				// AllowedInbounds initially nil → allow all
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-cr-change-in",
				Address: "10.0.4.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30452},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		customRoute := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: "default"},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
			},
		}
		Expect(k8sClient.Create(testCtx, customRoute)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, customRoute) })

		// First reconcile: AllowedInbounds is nil → inbound should collect the outbound
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("10.0.4.1"))

		// Change AllowedInbounds to exclude inboundName
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: outboundName, Namespace: "default"}, outboundNode)).To(Succeed())
		outboundNode.Spec.AllowedInbounds = []string{"some-other-inbound"}
		Expect(k8sClient.Update(testCtx, outboundNode)).To(Succeed())

		// Reconcile inbound — should now see the binding filter kick in
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)).To(Succeed())
		// After AllowedInbounds change, inbound should no longer see outbound in config
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("10.0.4.1"))
	})

	It("(e2) should NOT collect self-outbound node S for another inbound X when AllowedInbounds=[S]", func() {
		// Self-outbound node S: has both inbound+outbound roles, AllowedInbounds=[S] restricts to itself only.
		// Another inbound node X in the same region should NOT see outbound-S in its config.
		selfOutboundName := "test-self-outbound-s"
		inboundName := "test-inbound-x-e2"

		selfOutboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: selfOutboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:         "k8s-node-self-outbound",
				Address:         "10.0.5.1",
				Region:          "us-west",
				Roles:           []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound, proxyv1alpha1.ProxyRoleOutbound},
				RelayPort:       31970,
				AllowedInbounds: []string{selfOutboundName}, // Only allows itself
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30453},
				},
			},
		}
		selfOutboundNode.Spec.InboundProtocol = "vless"
		Expect(k8sClient.Create(testCtx, selfOutboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, selfOutboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: selfOutboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: selfOutboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-inbound-x-e2",
				Address: "10.0.5.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30454},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())

		// X is NOT in S's AllowedInbounds → X's config must NOT contain outbound-S
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("outbound-" + selfOutboundName))

		// Parse JSON and verify outbounds structure
		var config map[string]any
		Expect(json.Unmarshal([]byte(cm.Data["config.json"]), &config)).To(Succeed())

		outbounds, _ := config["outbounds"].([]any)
		tags := make(map[string]bool)
		for _, ob := range outbounds {
			if m, ok := ob.(map[string]any); ok {
				if tag, ok := m["tag"].(string); ok {
					tags[tag] = true
				}
			}
		}
		Expect(tags).To(HaveKey("direct"), "config outbounds must include a 'direct' outbound")
		Expect(tags).NotTo(HaveKey("outbound-"+selfOutboundName), "config must not include outbound for self-outbound node S")
	})

	It("(a_out) should collect outbound when AllowedOutbounds is empty (backward compat)", func() {
		outboundName := "test-ob-empty-allowed-out"
		inboundName := "test-ib-empty-allowed-out"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-empty-allowed-out",
				Address:   "20.0.0.1",
				Region:    "us-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31971,
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-ib-empty-allowed-out",
				Address: "20.0.0.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30460},
				},
				// AllowedOutbounds is nil/empty → allow all (backward compat)
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("20.0.0.1"))
	})

	It("(b_out) should collect outbound when AllowedOutbounds includes the outbound node", func() {
		outboundName := "test-ob-match-allowed-out"
		inboundName := "test-ib-match-allowed-out"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-match-allowed-out",
				Address:   "20.0.1.1",
				Region:    "us-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31972,
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-ib-match-allowed-out",
				Address: "20.0.1.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30461},
				},
				AllowedOutbounds: []string{outboundName},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// Outbound IS in the AllowedOutbounds list → config contains outbound IP
		Expect(cm.Data["config.json"]).To(ContainSubstring("20.0.1.1"))
	})

	It("(c_out) should NOT collect outbound when AllowedOutbounds excludes the outbound node", func() {
		outboundName := "test-ob-mismatch-allowed-out"
		inboundName := "test-ib-mismatch-allowed-out"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-mismatch-allowed-out",
				Address:   "20.0.2.1",
				Region:    "us-west",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31973,
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-ib-mismatch-allowed-out",
				Address: "20.0.2.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30462},
				},
				AllowedOutbounds: []string{"some-other-outbound"},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// Outbound is NOT in the AllowedOutbounds list → config must NOT contain outbound IP
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("20.0.2.1"))
	})

	It("(d_out) should skip CustomRoute when AllowedOutbounds excludes the outbound node", func() {
		// Two regions: inbound in us-west, outbound in us-east (cross-region, requires CustomRoute)
		outboundName := "test-ob-cr-mismatch-out"
		inboundName := "test-ib-cr-mismatch-out"
		routeName := "test-route-mismatch-out"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-cr-ob-mismatch-out",
				Address:   "20.0.3.1",
				Region:    "us-east",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31974,
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-cr-ib-mismatch-out",
				Address: "20.0.3.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30463},
				},
				AllowedOutbounds: []string{"some-other-outbound"},
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		customRoute := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: "default"},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
			},
		}
		Expect(k8sClient.Create(testCtx, customRoute)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, customRoute) })

		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		// CustomRoute is defined, but AllowedOutbounds excludes the outbound → config should NOT have outbound IP
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("20.0.3.1"))
	})

	It("(e_out) should reconcile inbound when AllowedOutbounds changes (cross-region)", func() {
		outboundName := "test-ob-cr-change-out"
		inboundName := "test-ib-cr-change-out"
		routeName := "test-route-change-out"

		outboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: outboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef:   "k8s-node-cr-change-ob-out",
				Address:   "20.0.4.1",
				Region:    "us-east",
				Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
				RelayPort: 31975,
			},
		}
		Expect(k8sClient.Create(testCtx, outboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, outboundNode) })

		_, err := reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: outboundName, Namespace: "default"},
		})

		inboundNode := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: inboundName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-cr-change-ib-out",
				Address: "20.0.4.2",
				Region:  "us-west",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30464},
				},
				// AllowedOutbounds initially nil → allow all
			},
		}
		Expect(k8sClient.Create(testCtx, inboundNode)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, inboundNode) })

		customRoute := &proxyv1alpha1.CustomRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: "default"},
			Spec: proxyv1alpha1.CustomRouteSpec{
				InboundNode:  inboundName,
				OutboundNode: outboundName,
			},
		}
		Expect(k8sClient.Create(testCtx, customRoute)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(testCtx, customRoute) })

		// First reconcile: AllowedOutbounds is nil → inbound should collect the outbound
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		cm := &corev1.ConfigMap{}
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(cm.Data["config.json"]).To(ContainSubstring("20.0.4.1"))

		// Change AllowedOutbounds to exclude outboundName
		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName, Namespace: "default"}, inboundNode)).To(Succeed())
		inboundNode.Spec.AllowedOutbounds = []string{"some-other-outbound"}
		Expect(k8sClient.Update(testCtx, inboundNode)).To(Succeed())

		// Reconcile inbound — should now see the whitelist filter kick in
		_, err = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})
		Expect(err).NotTo(HaveOccurred())
		_, _ = reconciler.Reconcile(testCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: inboundName, Namespace: "default"},
		})

		Expect(k8sClient.Get(testCtx, types.NamespacedName{Name: inboundName + "-config", Namespace: "default"}, cm)).To(Succeed())
		// After AllowedOutbounds change, inbound should no longer see outbound in config
		Expect(cm.Data["config.json"]).NotTo(ContainSubstring("20.0.4.1"))
	})

	It("should remove finalizer when SingBoxNode is deleted", func() {
		nodeName := "test-delete-node"
		node := &proxyv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: "default"},
			Spec: proxyv1alpha1.SingBoxNodeSpec{
				NodeRef: "k8s-node-7",
				Address: "7.8.9.0",
				Region:  "sa-east",
				Roles:   []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
				SupportedProtocols: []proxyv1alpha1.ProtocolConfig{
					{Protocol: "vless", Port: 30447},
				},
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

		deletedNode := &proxyv1alpha1.SingBoxNode{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: nodeName, Namespace: "default"}, deletedNode)
		if err == nil {
			Expect(deletedNode.Finalizers).NotTo(ContainElement(singboxNodeFinalizer))
		}
	})
})

// hostPortProtocols is retained here, in test scope only, for the legacy
// protocol-mapping tests in protocol_helpers_test.go. Production pods now run
// with hostNetwork and declare no hostPort entries at all.
func hostPortProtocols(protocol string) []corev1.Protocol {
	switch protocol {
	case "hysteria2", "tuic":
		return []corev1.Protocol{corev1.ProtocolUDP}
	case "socks5":
		return []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP}
	default:
		return []corev1.Protocol{corev1.ProtocolTCP}
	}
}
