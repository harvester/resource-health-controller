package harvesterhci

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	healthv1 "github.com/harvester/resource-health-controller/pkg/apis/health.harvesterhci.io/v1beta1"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/healthcheck"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/testutil"
	"github.com/harvester/resource-health-controller/pkg/util/fakeclients"
)

func TestReconcileSupportBundles(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		conditions []harvesterv1.Condition
		deleting   bool
		failed     bool
		info       bool
	}{
		{name: "new support bundle"},
		{name: "generating", state: "generating", info: true},
		{name: "ready", state: "ready", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.SupportBundleInitialized, Status: corev1.ConditionTrue},
		}},
		{name: "deleting support bundle is excluded", deleting: true, state: "error", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.SupportBundleInitialized, Status: corev1.ConditionFalse, Message: "timeout"},
		}},
		{name: "error state", failed: true, state: "error", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.SupportBundleInitialized, Status: corev1.ConditionFalse, Message: "fail to generate supportbundle: timeout"},
		}},
		{name: "initialized false without error state", failed: true, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.SupportBundleInitialized, Status: corev1.ConditionFalse, Message: "fail to generate supportbundle: timeout"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &harvesterv1.SupportBundle{
				ObjectMeta: metav1.ObjectMeta{Name: "bundle-a", Namespace: "harvester-system"},
				Status:     harvesterv1.SupportBundleStatus{State: tc.state, Progress: 40, Conditions: tc.conditions},
			}
			if tc.deleting {
				deletedAt := metav1.Now()
				bundle.DeletionTimestamp = &deletedAt
			}
			clientset, healthset := testutil.NewClients(bundle)
			writer := healthcheck.Writer{Client: fakeclients.ComponentHealthClient(healthset.HealthV1beta1().ComponentHealths)}
			bundles := &supportBundleCheck{writer: writer, supportBundleCache: fakeclients.SupportBundleCache(clientset.HarvesterhciV1beta1().SupportBundles)}
			if !assert.NoError(t, bundles.Reconcile()) {
				return
			}
			health, err := healthset.HealthV1beta1().ComponentHealths().Get(context.Background(), supportBundleComponentHealthName, metav1.GetOptions{})
			if !assert.NoError(t, err) {
				return
			}
			if tc.info {
				assert.Len(t, health.Status.Checks, 1)
				check := health.Status.Checks[checkKeySupportBundleGenerating]
				assert.Equal(t, healthv1.SeverityInfo, check.Severity)
				assert.Equal(t, 1, check.AffectedCount)
				assert.Contains(t, check.AffectedResources.Names, "harvester-system/bundle-a")
				return
			}
			if !tc.failed {
				assert.Empty(t, health.Status.Checks)
				return
			}
			assert.Len(t, health.Status.Checks, 1)
			check := health.Status.Checks[checkKeySupportBundleFailed]
			assert.Equal(t, healthv1.SeverityError, check.Severity)
			assert.Equal(t, 1, check.AffectedCount)
			assert.Equal(t, supportBundleKind, check.AffectedResources.Kind)
			assert.Equal(t, harvesterv1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
			assert.Contains(t, check.AffectedResources.Names, "harvester-system/bundle-a")
			assert.Contains(t, check.Message, `message="fail to generate supportbundle: timeout"`)
		})
	}
}

func TestSupportBundleChecksClearAndPreserveNamespacedResources(t *testing.T) {
	ctx := context.Background()
	bundle := &harvesterv1.SupportBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "bundle-a", Namespace: "harvester-system"},
		Status:     harvesterv1.SupportBundleStatus{State: "error"},
	}
	other := bundle.DeepCopy()
	other.Name = "bundle-b"
	clientset, healthset := testutil.NewClients(bundle, other)
	controller := &testutil.RecordingController{}
	writer := healthcheck.Writer{
		Controller: controller,
		Client:     fakeclients.ComponentHealthClient(healthset.HealthV1beta1().ComponentHealths),
	}
	bundles := &supportBundleCheck{writer: writer, supportBundleCache: fakeclients.SupportBundleCache(clientset.HarvesterhciV1beta1().SupportBundles)}
	result, err := bundles.OnSupportBundleChanged("harvester-system/bundle-a", bundle)
	assert.Same(t, bundle, result)
	assert.NoError(t, err)
	assert.Equal(t, []string{supportBundleComponentHealthName}, controller.Enqueued)
	if !assert.NoError(t, bundles.Reconcile()) {
		return
	}
	health, err := healthset.HealthV1beta1().ComponentHealths().Get(ctx, supportBundleComponentHealthName, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, 2, health.Status.Checks[checkKeySupportBundleFailed].AffectedCount)

	bundle.Status.State = "ready"
	if _, err := clientset.HarvesterhciV1beta1().SupportBundles(bundle.Namespace).Update(ctx, bundle, metav1.UpdateOptions{}); !assert.NoError(t, err) {
		return
	}
	if !assert.NoError(t, bundles.Reconcile()) {
		return
	}
	health, err = healthset.HealthV1beta1().ComponentHealths().Get(ctx, supportBundleComponentHealthName, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	check := health.Status.Checks[checkKeySupportBundleFailed]
	assert.Equal(t, 1, check.AffectedCount)
	assert.NotContains(t, check.AffectedResources.Names, "harvester-system/bundle-a")

	if !assert.NoError(t, clientset.HarvesterhciV1beta1().SupportBundles(other.Namespace).Delete(ctx, other.Name, metav1.DeleteOptions{})) {
		return
	}
	result, err = bundles.OnSupportBundleChanged("harvester-system/bundle-b", nil)
	assert.Nil(t, result)
	assert.NoError(t, err)
	if !assert.NoError(t, bundles.Reconcile()) {
		return
	}
	health, err = healthset.HealthV1beta1().ComponentHealths().Get(ctx, supportBundleComponentHealthName, metav1.GetOptions{})
	if assert.NoError(t, err) {
		assert.Empty(t, health.Status.Checks)
	}
}

func TestReconcileSupportBundlesListError(t *testing.T) {
	clientset, _ := testutil.NewClients()
	clientset.PrependReactor("list", "supportbundles", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("cache failure")
	})
	bundles := &supportBundleCheck{supportBundleCache: fakeclients.SupportBundleCache(clientset.HarvesterhciV1beta1().SupportBundles)}
	assert.EqualError(t, bundles.Reconcile(), "failed to list SupportBundles: cache failure")
}
