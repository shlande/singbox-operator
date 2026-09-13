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

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	singboxoperatorv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

var _ = Describe("ExternalOutbound Webhook", func() {
	var (
		obj       *singboxoperatorv1alpha1.ExternalOutbound
		oldObj    *singboxoperatorv1alpha1.ExternalOutbound
		validator ExternalOutboundValidator
	)

	newExternalOutbound := func(mutate func(*singboxoperatorv1alpha1.ExternalOutbound)) *singboxoperatorv1alpha1.ExternalOutbound {
		eob := &singboxoperatorv1alpha1.ExternalOutbound{
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

	BeforeEach(func() {
		obj = &singboxoperatorv1alpha1.ExternalOutbound{}
		oldObj = &singboxoperatorv1alpha1.ExternalOutbound{}
		validator = ExternalOutboundValidator{}
		Expect(validator).NotTo(BeNil(), "Expected validator to be initialized")
		Expect(oldObj).NotTo(BeNil(), "Expected oldObj to be initialized")
		Expect(obj).NotTo(BeNil(), "Expected obj to be initialized")
	})

	Context("When creating or updating ExternalOutbound under Validating Webhook", func() {
		DescribeTable("stateless spec validation",
			func(mutate func(*singboxoperatorv1alpha1.ExternalOutbound), expectValid bool) {
				obj = newExternalOutbound(mutate)

				_, err := validator.ValidateCreate(ctx, obj)
				if expectValid {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}

				_, err = validator.ValidateUpdate(ctx, oldObj, obj)
				if expectValid {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			},
			Entry("accepts socks5 without tls and without credentials",
				nil,
				true),
			Entry("accepts socks5 with optional credentialsSecretRef",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "socks-creds"}
				},
				true),
			Entry("accepts http without tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHTTP
				},
				true),
			Entry("accepts http with tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHTTP
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				},
				true),
			Entry("rejects socks5 with tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				},
				false),
			Entry("rejects shadowsocks with tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "ss-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{Insecure: true}
				},
				false),
			Entry("rejects trojan without tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "trojan-creds"}
				},
				false),
			Entry("rejects trojan tls without serverName and insecure",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "trojan-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{}
				},
				false),
			Entry("accepts trojan tls with serverName",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "trojan-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				},
				true),
			Entry("accepts hysteria2 tls with insecure only",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHysteria2
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "hy2-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{Insecure: true}
				},
				true),
			Entry("rejects anytls without tls",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolAnyTLS
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "anytls-creds"}
				},
				false),
			Entry("accepts tuic with tls and credentialsSecretRef",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "tuic-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				},
				true),
			Entry("rejects missing credentialsSecretRef for shadowsocks",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolShadowsocks
				},
				false),
			Entry("rejects missing credentialsSecretRef for tuic",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTUIC
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
				},
				false),
			Entry("rejects hysteria2 options on trojan",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "trojan-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
					eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{UpMbps: 100}
				},
				false),
			Entry("accepts hysteria2 options on hysteria2",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolHysteria2
					eob.Spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "hy2-creds"}
					eob.Spec.TLS = &singboxoperatorv1alpha1.ExternalOutboundTLS{ServerName: "cdn.example.com"}
					eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{UpMbps: 100, DownMbps: 200, Obfs: true}
				},
				true),
			Entry("rejects multiple violations at once",
				func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
					eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
					eob.Spec.Hysteria2 = &singboxoperatorv1alpha1.Hysteria2Options{}
				},
				false),
		)

		It("Should allow deletion unconditionally", func() {
			obj = newExternalOutbound(func(eob *singboxoperatorv1alpha1.ExternalOutbound) {
				eob.Spec.Protocol = singboxoperatorv1alpha1.ExternalProtocolTrojan
			})
			_, err := validator.ValidateDelete(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
