/*
Copyright 2026 Uptime.com.

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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/uptime-com/uptime-client-go/v2/pkg/upapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	monitoringv1alpha1 "github.com/uptime-com/uptime-operator/api/v1alpha1"
)

var _ = Describe("UptimeCheck Controller", func() {
	const (
		resourceName = "test-uptimecheck"
		ns           = "default"
	)

	var key = types.NamespacedName{Name: resourceName, Namespace: ns}

	AfterEach(func() {
		ctx := context.Background()
		cr := &monitoringv1alpha1.UptimeCheck{}
		if err := k8sClient.Get(ctx, key, cr); apierrors.IsNotFound(err) {
			return
		}
		cr.Finalizers = nil
		_ = k8sClient.Update(ctx, cr)
		_ = k8sClient.Delete(ctx, cr)
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key, &monitoringv1alpha1.UptimeCheck{}))
		}).Should(BeTrue())
	})

	It("adds the finalizer on first reconcile without calling the Uptime API", func() {
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, &monitoringv1alpha1.UptimeCheck{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns},
			Spec: monitoringv1alpha1.UptimeCheckSpec{
				Type:              monitoringv1alpha1.CheckTypeHTTP,
				APITokenSecretRef: &monitoringv1alpha1.SecretKeyReference{Name: "uptime-token"},
				HTTP:              &monitoringv1alpha1.HTTPSpec{URL: "https://example.com/health"},
			},
		})).To(Succeed())

		apiCalls := 0
		reconciler := &UptimeCheckReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			NewAPI: func(context.Context, client.Client, string, string, string, string) (upapi.API, error) {
				apiCalls++
				return nil, errors.New("should not be called before finalizer is added")
			},
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(apiCalls).To(Equal(0))

		got := &monitoringv1alpha1.UptimeCheck{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		Expect(got.Finalizers).To(ContainElement(monitoringv1alpha1.Finalizer))
	})

	It("reports TokenUnavailable when the Secret is missing on subsequent reconcile", func() {
		ctx := context.Background()
		cr := &monitoringv1alpha1.UptimeCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:       resourceName,
				Namespace:  ns,
				Finalizers: []string{monitoringv1alpha1.Finalizer},
			},
			Spec: monitoringv1alpha1.UptimeCheckSpec{
				Type:              monitoringv1alpha1.CheckTypeHTTP,
				APITokenSecretRef: &monitoringv1alpha1.SecretKeyReference{Name: "missing-secret"},
				HTTP:              &monitoringv1alpha1.HTTPSpec{URL: "https://example.com/health"},
			},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		reconciler := &UptimeCheckReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			NewAPI: func(context.Context, client.Client, string, string, string, string) (upapi.API, error) {
				return nil, errors.New("secret not found")
			},
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(HaveOccurred())

		got := &monitoringv1alpha1.UptimeCheck{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		Expect(got.Status.Conditions).NotTo(BeEmpty())
		Expect(got.Status.Conditions[0].Type).To(Equal(monitoringv1alpha1.ConditionTypeReady))
		Expect(got.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
		Expect(got.Status.Conditions[0].Reason).To(Equal("TokenUnavailable"))
	})

	It("falls back to the operator's default token Secret when the spec omits the ref", func() {
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, &monitoringv1alpha1.UptimeCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:       resourceName,
				Namespace:  ns,
				Finalizers: []string{monitoringv1alpha1.Finalizer},
			},
			Spec: monitoringv1alpha1.UptimeCheckSpec{
				Type: monitoringv1alpha1.CheckTypeHTTP,
				HTTP: &monitoringv1alpha1.HTTPSpec{URL: "https://example.com/health"},
			},
		})).To(Succeed())

		var gotNS, gotName, gotKey string
		reconciler := &UptimeCheckReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			DefaultTokenSecret: DefaultTokenSecret{
				Namespace: "uptime-operator-system",
				Name:      "operator-default-token",
				Key:       "api-token",
			},
			NewAPI: func(
				_ context.Context, _ client.Client, namespace, secret, secretKey, _ string,
			) (upapi.API, error) {
				gotNS, gotName, gotKey = namespace, secret, secretKey
				return nil, errors.New("stop before calling the API")
			},
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(HaveOccurred())
		Expect(gotNS).To(Equal("uptime-operator-system"))
		Expect(gotName).To(Equal("operator-default-token"))
		Expect(gotKey).To(Equal("api-token"))
	})

	It("reports TokenNotConfigured when neither the spec nor the operator names a Secret", func() {
		ctx := context.Background()
		Expect(k8sClient.Create(ctx, &monitoringv1alpha1.UptimeCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:       resourceName,
				Namespace:  ns,
				Finalizers: []string{monitoringv1alpha1.Finalizer},
			},
			Spec: monitoringv1alpha1.UptimeCheckSpec{
				Type: monitoringv1alpha1.CheckTypeHTTP,
				HTTP: &monitoringv1alpha1.HTTPSpec{URL: "https://example.com/health"},
			},
		})).To(Succeed())

		apiCalls := 0
		reconciler := &UptimeCheckReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			NewAPI: func(context.Context, client.Client, string, string, string, string) (upapi.API, error) {
				apiCalls++
				return nil, errors.New("should not be called without a token source")
			},
		}

		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(MatchError(ErrNoAPIToken))
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(apiCalls).To(Equal(0))

		got := &monitoringv1alpha1.UptimeCheck{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		Expect(got.Status.Conditions).NotTo(BeEmpty())
		Expect(got.Status.Conditions[0].Reason).To(Equal("TokenNotConfigured"))
	})
})
