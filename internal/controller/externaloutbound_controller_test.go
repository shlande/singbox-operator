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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	singboxoperatorv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

var _ = Describe("ExternalOutbound Controller", func() {
	const resourceNamespace = "default"

	ctx := context.Background()

	var reconciler *ExternalOutboundReconciler

	BeforeEach(func() {
		reconciler = &ExternalOutboundReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	AfterEach(func() {
		By("cleaning up all ExternalOutbounds created by the test")
		eobList := &singboxoperatorv1alpha1.ExternalOutboundList{}
		Expect(k8sClient.List(ctx, eobList, client.InNamespace(resourceNamespace))).To(Succeed())
		for i := range eobList.Items {
			Expect(k8sClient.Delete(ctx, &eobList.Items[i])).To(Succeed())
		}

		By("cleaning up all credential Secrets created by the test")
		secretList := &corev1.SecretList{}
		Expect(k8sClient.List(ctx, secretList, client.InNamespace(resourceNamespace),
			client.MatchingLabels{"singboxoperator.shlande.top/test": "externaloutbound"})).To(Succeed())
		for i := range secretList.Items {
			Expect(k8sClient.Delete(ctx, &secretList.Items[i])).To(Succeed())
		}

		By("cleaning up the name-conflict SingBoxNode if present")
		node := &singboxoperatorv1alpha1.SingBoxNode{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "conflict-eob", Namespace: resourceNamespace}, node); err == nil {
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		} else {
			Expect(errors.IsNotFound(err)).To(BeTrue())
		}
	})

	newExternalOutbound := func(name string, mutate func(*singboxoperatorv1alpha1.ExternalOutbound)) *singboxoperatorv1alpha1.ExternalOutbound {
		eob := &singboxoperatorv1alpha1.ExternalOutbound{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: resourceNamespace,
			},
			Spec: singboxoperatorv1alpha1.ExternalOutboundSpec{
				Protocol: singboxoperatorv1alpha1.ExternalProtocolSocks5,
				Server:   "proxy.example.com",
				Port:     1080,
			},
		}
		if mutate != nil {
			mutate(eob)
		}
		return eob
	}

	newCredentialSecret := func(name string, data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: resourceNamespace,
				Labels:    map[string]string{"singboxoperator.shlande.top/test": "externaloutbound"},
			},
			Data: data,
		}
	}

	secretRef := func(name string) *corev1.LocalObjectReference {
		return &corev1.LocalObjectReference{Name: name}
	}

	reconcileCondition := func(eob *singboxoperatorv1alpha1.ExternalOutbound) *metav1.Condition {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: eob.Name, Namespace: eob.Namespace},
		})
		Expect(err).NotTo(HaveOccurred())

		updated := &singboxoperatorv1alpha1.ExternalOutbound{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: eob.Name, Namespace: eob.Namespace}, updated)).To(Succeed())
		Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))

		cond := apimeta.FindStatusCondition(updated.Status.Conditions,
			singboxoperatorv1alpha1.ExternalOutboundAcceptedConditionType)
		Expect(cond).NotTo(BeNil())
		return cond
	}

	DescribeTable("spec coherence checks",
		func(name string, mutate func(*singboxoperatorv1alpha1.ExternalOutbound), expectedStatus metav1.ConditionStatus, expectedReason string) {
			eob := newExternalOutbound(name, mutate)
			Expect(k8sClient.Create(ctx, eob)).To(Succeed())

			cond := reconcileCondition(eob)
			Expect(cond.Status).To(Equal(expectedStatus))
			Expect(cond.Reason).To(Equal(expectedReason))
			Expect(cond.ObservedGeneration).To(Equal(eob.Generation))
		},
		Entry("accepts socks5 without credentials secret",
			"eob-spec-socks5", nil,
			metav1.ConditionTrue, "Valid"),
		Entry("accepts http without tls and without credentials secret",
			"eob-spec-http",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHTTP
			},
			metav1.ConditionTrue, "Valid"),
		Entry("accepts http with optional tls",
			"eob-spec-http-tls",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHTTP
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			metav1.ConditionTrue, "Valid"),
		Entry("rejects socks5 with tls",
			"eob-spec-socks5-tls",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("rejects shadowsocks with tls",
			"eob-spec-ss-tls",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
				eob.Spec.CredentialsSecretRef = secretRef("ss-creds")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{Insecure: true}
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("rejects trojan without tls",
			"eob-spec-trojan-notls",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
				eob.Spec.CredentialsSecretRef = secretRef("trojan-creds")
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("rejects hysteria2 tls without serverName and insecure",
			"eob-spec-hy2-sniname",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHysteria2
				eob.Spec.CredentialsSecretRef = secretRef("hy2-creds")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{}
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("accepts tuic with insecure tls",
			"eob-spec-tuic-insecure",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{Insecure: true}
				// No credentialsSecretRef: InvalidSpec must fire before secret checks.
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("rejects missing credentialsSecretRef for trojan",
			"eob-spec-trojan-noref",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			metav1.ConditionFalse, "InvalidSpec"),
		Entry("rejects hysteria2 options on non-hysteria2 protocol",
			"eob-spec-hy2opts",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
				eob.Spec.CredentialsSecretRef = secretRef("trojan-creds")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{UpMbps: 100}
			},
			metav1.ConditionFalse, "InvalidSpec"),
	)

	DescribeTable("credential secret checks",
		func(name string, mutate func(*singboxoperatorv1alpha1.ExternalOutbound), secret *corev1.Secret, expectedStatus metav1.ConditionStatus, expectedReason string) {
			if secret != nil {
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}
			eob := newExternalOutbound(name, mutate)
			Expect(k8sClient.Create(ctx, eob)).To(Succeed())

			cond := reconcileCondition(eob)
			Expect(cond.Status).To(Equal(expectedStatus))
			Expect(cond.Reason).To(Equal(expectedReason))
		},
		Entry("rejects missing secret",
			"eob-cred-missing",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
				eob.Spec.CredentialsSecretRef = secretRef("trojan-creds-missing")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			nil,
			metav1.ConditionFalse, "SecretNotFound"),
		Entry("rejects tuic secret missing uuid",
			"eob-cred-tuic-uuid",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
				eob.Spec.CredentialsSecretRef = secretRef("tuic-creds")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			newCredentialSecret("tuic-creds", map[string][]byte{
				"password": []byte("s3cret"),
			}),
			metav1.ConditionFalse, "SecretKeyMissing"),
		Entry("rejects tuic secret with empty password value",
			"eob-cred-tuic-empty",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
				eob.Spec.CredentialsSecretRef = secretRef("tuic-creds-empty")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			newCredentialSecret("tuic-creds-empty", map[string][]byte{
				"uuid":     []byte("00000000-0000-0000-0000-000000000000"),
				"password": {},
			}),
			metav1.ConditionFalse, "SecretKeyMissing"),
		Entry("accepts tuic with complete secret",
			"eob-cred-tuic-ok",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
				eob.Spec.CredentialsSecretRef = secretRef("tuic-creds-ok")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
			},
			newCredentialSecret("tuic-creds-ok", map[string][]byte{
				"uuid":     []byte("00000000-0000-0000-0000-000000000000"),
				"password": []byte("s3cret"),
			}),
			metav1.ConditionTrue, "Valid"),
		Entry("rejects hysteria2 obfs without obfsPassword",
			"eob-cred-hy2-obfs",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHysteria2
				eob.Spec.CredentialsSecretRef = secretRef("hy2-creds-obfs")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{Obfs: true}
			},
			newCredentialSecret("hy2-creds-obfs", map[string][]byte{
				"password": []byte("s3cret"),
			}),
			metav1.ConditionFalse, "SecretKeyMissing"),
		Entry("accepts hysteria2 obfs with obfsPassword",
			"eob-cred-hy2-obfs-ok",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHysteria2
				eob.Spec.CredentialsSecretRef = secretRef("hy2-creds-obfs-ok")
				eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{Obfs: true}
			},
			newCredentialSecret("hy2-creds-obfs-ok", map[string][]byte{
				"password":     []byte("s3cret"),
				"obfsPassword": []byte("obfs"),
			}),
			metav1.ConditionTrue, "Valid"),
		Entry("rejects shadowsocks secret missing method",
			"eob-cred-ss-method",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
				eob.Spec.CredentialsSecretRef = secretRef("ss-creds-method")
			},
			newCredentialSecret("ss-creds-method", map[string][]byte{
				"password": []byte("s3cret"),
			}),
			metav1.ConditionFalse, "SecretKeyMissing"),
		Entry("rejects unsupported shadowsocks method",
			"eob-cred-ss-badmethod",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
				eob.Spec.CredentialsSecretRef = secretRef("ss-creds-badmethod")
			},
			newCredentialSecret("ss-creds-badmethod", map[string][]byte{
				"method":   []byte("aes-256-cfb"),
				"password": []byte("s3cret"),
			}),
			metav1.ConditionFalse, "InvalidMethod"),
		Entry("accepts whitelisted shadowsocks method",
			"eob-cred-ss-ok",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
				eob.Spec.CredentialsSecretRef = secretRef("ss-creds-ok")
			},
			newCredentialSecret("ss-creds-ok", map[string][]byte{
				"method":   []byte("2022-blake3-aes-256-gcm"),
				"password": []byte("s3cret"),
			}),
			metav1.ConditionTrue, "Valid"),
		Entry("rejects socks5 secret with username only",
			"eob-cred-socks-half",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.CredentialsSecretRef = secretRef("socks-creds-half")
			},
			newCredentialSecret("socks-creds-half", map[string][]byte{
				"username": []byte("user"),
			}),
			metav1.ConditionFalse, "SecretKeyMissing"),
		Entry("accepts socks5 secret with both username and password",
			"eob-cred-socks-both",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.CredentialsSecretRef = secretRef("socks-creds-both")
			},
			newCredentialSecret("socks-creds-both", map[string][]byte{
				"username": []byte("user"),
				"password": []byte("s3cret"),
			}),
			metav1.ConditionTrue, "Valid"),
		Entry("accepts socks5 secret with neither username nor password",
			"eob-cred-socks-none",
			func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.CredentialsSecretRef = secretRef("socks-creds-none")
			},
			newCredentialSecret("socks-creds-none", map[string][]byte{
				"unrelated": []byte("data"),
			}),
			metav1.ConditionTrue, "Valid"),
	)

	It("should reject the ExternalOutbound when a SingBoxNode with the same name exists", func() {
		node := &singboxoperatorv1alpha1.SingBoxNode{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "conflict-eob",
				Namespace: resourceNamespace,
			},
			Spec: singboxoperatorv1alpha1.SingBoxNodeSpec{
				NodeRef: "worker-1",
				Address: "203.0.113.10",
				Region:  "us-west",
				Roles:   []singboxoperatorv1alpha1.ProxyRole{singboxoperatorv1alpha1.ProxyRoleOutbound},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		eob := newExternalOutbound("conflict-eob", nil)
		Expect(k8sClient.Create(ctx, eob)).To(Succeed())

		cond := reconcileCondition(eob)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("NameConflict"))

		By("recovering once the SingBoxNode is removed")
		Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		cond = reconcileCondition(eob)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Valid"))
	})

	It("should refresh the condition when the referenced secret changes", func() {
		secret := newCredentialSecret("rotating-creds", map[string][]byte{
			"password": []byte("s3cret"),
		})
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		eob := newExternalOutbound("rotating-eob", func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
			eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
			eob.Spec.CredentialsSecretRef = secretRef("rotating-creds")
			eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
		})
		Expect(k8sClient.Create(ctx, eob)).To(Succeed())

		cond := reconcileCondition(eob)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("SecretKeyMissing"))

		By("completing the credentials and reconciling again")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rotating-creds", Namespace: resourceNamespace}, secret)).To(Succeed())
		secret.Data["uuid"] = []byte("00000000-0000-0000-0000-000000000000")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		cond = reconcileCondition(eob)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Valid"))
	})

	It("should map a Secret to every ExternalOutbound referencing it", func() {
		secret := newCredentialSecret("mapped-creds", nil)

		matching := newExternalOutbound("mapped-matching", func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
			eob.Spec.CredentialsSecretRef = secretRef("mapped-creds")
		})
		other := newExternalOutbound("mapped-other", func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
			eob.Spec.CredentialsSecretRef = secretRef("elsewhere")
		})
		anonymous := newExternalOutbound("mapped-anonymous", nil)

		fakeList := &singboxoperatorv1alpha1.ExternalOutboundList{
			Items: []singboxoperatorv1alpha1.ExternalOutbound{*matching, *other, *anonymous},
		}
		fakeClient := &fakeEOBListClient{Client: k8sClient, list: fakeList}

		mapper := &ExternalOutboundReconciler{Client: fakeClient, Scheme: k8sClient.Scheme()}
		requests := mapper.credentialsSecretMapper(ctx, secret)

		Expect(requests).To(ConsistOf(
			reconcile.Request{NamespacedName: types.NamespacedName{Name: "mapped-matching", Namespace: resourceNamespace}},
		))
	})
})

// fakeEOBListClient stubs ExternalOutbound List calls for mapper unit tests.
type fakeEOBListClient struct {
	client.Client
	list *singboxoperatorv1alpha1.ExternalOutboundList
}

func (f *fakeEOBListClient) List(_ context.Context, obj client.ObjectList, _ ...client.ListOption) error {
	if list, ok := obj.(*singboxoperatorv1alpha1.ExternalOutboundList); ok {
		f.list.DeepCopyInto(list)
	}
	return nil
}
