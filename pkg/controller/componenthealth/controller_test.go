package componenthealth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	healthv1 "github.com/harvester/resource-health-controller/pkg/apis/health.harvesterhci.io/v1beta1"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/core"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/harvesterhci"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/healthcheck"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/kubevirt"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/longhorn"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/testutil"
	"github.com/harvester/resource-health-controller/pkg/util/fakeclients"
)

// componentHealthNames are the ComponentHealth names that users and ignore rules see, in registration order.
var componentHealthNames = []string{
	"harvester-controller-node",
	"harvester-controller-vm",
	"harvester-controller-volume",
	"harvester-controller-vmbackup",
	"harvester-controller-schedulevmbackup",
	"harvester-controller-vmimage",
	"harvester-controller-upgrade",
	"harvester-controller-addon",
	"harvester-controller-supportbundle",
	"harvester-controller-upgradelog",
	"harvester-controller-volumeremotebackup",
	"harvester-controller-volumeremoterestore",
}

const (
	vmBackupComponentHealthName = "harvester-controller-vmbackup"
	checkKeyVMBackupFailed      = "VMBackupFailed"
)

// newTestHealthChecks returns every check with no caches, for tests that only need the check names.
func newTestHealthChecks(writer healthcheck.Writer) []healthcheck.Check {
	return []healthcheck.Check{
		core.NewNodeCheck(writer, nil),
		kubevirt.NewVMICheck(writer, nil),
		longhorn.NewVolumeCheck(writer, nil),
		harvesterhci.NewVMBackupCheck(writer, nil),
		harvesterhci.NewScheduleVMBackupCheck(writer, nil),
		harvesterhci.NewVMImageCheck(writer, nil),
		harvesterhci.NewUpgradeCheck(writer, nil),
		harvesterhci.NewAddonCheck(writer, nil),
		harvesterhci.NewSupportBundleCheck(writer, nil),
		harvesterhci.NewUpgradeLogCheck(writer, nil),
		harvesterhci.NewVolumeRemoteBackupCheck(writer, nil),
		harvesterhci.NewVolumeRemoteRestoreCheck(writer, nil),
	}
}

func TestTestHealthChecksMatchRegistrations(t *testing.T) {
	checks := newTestHealthChecks(healthcheck.Writer{})

	assert.Len(t, checks, len(registrations))
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name())
	}
	assert.Equal(t, componentHealthNames, names)
}

func TestEnqueueInitialHealth(t *testing.T) {
	controller := &testutil.RecordingController{}
	handler := &Handler{
		componentHealthController: controller,
		healthCachesSynced:        []cache.InformerSynced{func() bool { return true }},
	}
	handler.checks = newTestHealthChecks(healthcheck.Writer{})
	handler.enqueueInitialHealth(context.Background())
	assert.Equal(t, componentHealthNames, controller.Enqueued)
}

func TestEnqueueInitialHealthWaitsForCachesAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	controller := &testutil.RecordingController{}
	checked := make(chan struct{}, 1)
	handler := &Handler{
		componentHealthController: controller,
		healthCachesSynced: []cache.InformerSynced{func() bool {
			select {
			case checked <- struct{}{}:
			default:
			}
			return false
		}},
	}
	done := make(chan struct{})
	go func() {
		handler.enqueueInitialHealth(ctx)
		close(done)
	}()
	select {
	case <-checked:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not wait for cache synchronization")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not stop on cancellation")
	}
	assert.Empty(t, controller.Enqueued)
}

func TestOnComponentHealthChangedDispatch(t *testing.T) {
	for _, name := range componentHealthNames {
		t.Run(name, func(t *testing.T) {
			clientset, healthset := testutil.NewClients()
			writer := healthcheck.Writer{Client: fakeclients.ComponentHealthClient(healthset.HealthV1beta1().ComponentHealths)}
			handler := &Handler{
				healthCachesSynced: []cache.InformerSynced{func() bool { return true }},
			}
			handler.checks = []healthcheck.Check{
				core.NewNodeCheck(writer, fakeclients.NodeCache(clientset.CoreV1().Nodes)),
				kubevirt.NewVMICheck(writer, fakeclients.VirtualMachineInstanceCache(clientset.KubevirtV1().VirtualMachineInstances)),
				longhorn.NewVolumeCheck(writer, fakeclients.LonghornVolumeCache(clientset.LonghornV1beta2().Volumes)),
				harvesterhci.NewVMBackupCheck(writer, fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups)),
				harvesterhci.NewScheduleVMBackupCheck(writer, fakeclients.SVMBackupCache(clientset.HarvesterhciV1beta1().ScheduleVMBackups)),
				harvesterhci.NewVMImageCheck(writer, fakeclients.VirtualMachineImageCache(clientset.HarvesterhciV1beta1().VirtualMachineImages)),
				harvesterhci.NewUpgradeCheck(writer, fakeclients.UpgradeCache(clientset.HarvesterhciV1beta1().Upgrades)),
				harvesterhci.NewAddonCheck(writer, fakeclients.AddonCache(clientset.HarvesterhciV1beta1().Addons)),
				harvesterhci.NewSupportBundleCheck(writer, fakeclients.SupportBundleCache(clientset.HarvesterhciV1beta1().SupportBundles)),
				harvesterhci.NewUpgradeLogCheck(writer, fakeclients.UpgradeLogCache(clientset.HarvesterhciV1beta1().UpgradeLogs)),
				harvesterhci.NewVolumeRemoteBackupCheck(writer, fakeclients.VolumeRemoteBackupCache(clientset.HarvesterhciV1beta1().VolumeRemoteBackups)),
				harvesterhci.NewVolumeRemoteRestoreCheck(writer, fakeclients.VolumeRemoteRestoreCache(clientset.HarvesterhciV1beta1().VolumeRemoteRestores)),
			}
			result, err := handler.OnComponentHealthChanged(name, nil)
			assert.Nil(t, result)
			if !assert.NoError(t, err) {
				return
			}
			health, err := healthset.HealthV1beta1().ComponentHealths().Get(context.Background(), name, metav1.GetOptions{})
			if !assert.NoError(t, err) {
				return
			}
			assert.Empty(t, health.Status.Checks)
			result, err = handler.OnComponentHealthChanged(name, health)
			assert.Same(t, health, result)
			assert.NoError(t, err)
			for _, action := range healthset.Actions() {
				assert.False(t, action.Matches("update", "componenthealths"))
			}
			healths, err := healthset.HealthV1beta1().ComponentHealths().List(context.Background(), metav1.ListOptions{})
			if assert.NoError(t, err) {
				assert.Len(t, healths.Items, 1)
			}
		})
	}
}

func TestOnComponentHealthChangedWaitsForCaches(t *testing.T) {
	handler := &Handler{
		healthCachesSynced: []cache.InformerSynced{
			func() bool { return true },
			func() bool { return false },
		},
	}
	handler.checks = newTestHealthChecks(healthcheck.Writer{})
	for _, check := range handler.checks {
		t.Run(check.Name(), func(t *testing.T) {
			health := &healthv1.ComponentHealth{ObjectMeta: metav1.ObjectMeta{Name: check.Name()}}
			result, err := handler.OnComponentHealthChanged(check.Name(), health)
			assert.Same(t, health, result)
			assert.EqualError(t, err, "ComponentHealth resource caches are not synced")
		})
	}
}

func TestOnComponentHealthChangedIgnoresUnownedKeys(t *testing.T) {
	handler := &Handler{
		healthCachesSynced: []cache.InformerSynced{func() bool {
			t.Fatal("unowned keys must not check cache synchronization")
			return false
		}},
	}
	health := &healthv1.ComponentHealth{ObjectMeta: metav1.ObjectMeta{Name: "other-component"}}
	result, err := handler.OnComponentHealthChanged(health.Name, health)
	assert.Same(t, health, result)
	assert.NoError(t, err)
	result, err = handler.OnComponentHealthChanged(health.Name, nil)
	assert.Nil(t, result)
	assert.NoError(t, err)
}

func TestOnComponentHealthChangedReturnsReconcileErrors(t *testing.T) {
	clientset, _ := testutil.NewClients()
	clientset.PrependReactor("list", "virtualmachinebackups", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("temporary cache failure")
	})
	handler := &Handler{}
	handler.checks = []healthcheck.Check{
		harvesterhci.NewVMBackupCheck(healthcheck.Writer{}, fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups)),
	}
	result, err := handler.OnComponentHealthChanged(vmBackupComponentHealthName, nil)
	assert.Nil(t, result)
	assert.EqualError(t, err, "failed to list VirtualMachineBackups: temporary cache failure")
}

func TestOnComponentHealthChangedStopsUpdatingAfterChecksClear(t *testing.T) {
	health := &healthv1.ComponentHealth{
		ObjectMeta: metav1.ObjectMeta{Name: vmBackupComponentHealthName},
		Status: healthv1.ComponentHealthStatus{
			Checks: map[string]healthv1.CheckResult{
				checkKeyVMBackupFailed: {Severity: healthv1.SeverityError, Message: "stale backup failure"},
			},
		},
	}
	clientset, healthset := testutil.NewClients(health)
	handler := &Handler{}
	writer := healthcheck.Writer{Client: fakeclients.ComponentHealthClient(healthset.HealthV1beta1().ComponentHealths)}
	handler.checks = []healthcheck.Check{
		harvesterhci.NewVMBackupCheck(writer, fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups)),
	}
	_, err := handler.OnComponentHealthChanged(health.Name, health)
	if !assert.NoError(t, err) {
		return
	}
	updated, err := healthset.HealthV1beta1().ComponentHealths().Get(context.Background(), health.Name, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	assert.Empty(t, updated.Status.Checks)
	_, err = handler.OnComponentHealthChanged(updated.Name, updated)
	assert.NoError(t, err)
	updateCalls := 0
	for _, action := range healthset.Actions() {
		if action.Matches("update", "componenthealths") {
			updateCalls++
		}
	}
	assert.Equal(t, 1, updateCalls)
}

func TestOnIgnoreRuleChangedEnqueuesEveryComponentHealth(t *testing.T) {
	controller := &testutil.RecordingController{}
	handler := &Handler{componentHealthController: controller}
	handler.checks = newTestHealthChecks(healthcheck.Writer{})

	// a deleted rule is seen as nil
	result, err := handler.OnIgnoreRuleChanged("rule", nil)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, componentHealthNames, controller.Enqueued)
}
