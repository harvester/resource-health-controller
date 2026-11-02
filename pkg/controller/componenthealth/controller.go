package componenthealth

import (
	"context"
	"fmt"

	"k8s.io/client-go/tools/cache"

	healthv1 "github.com/harvester/resource-health-controller/pkg/apis/health.harvesterhci.io/v1beta1"
	"github.com/harvester/resource-health-controller/pkg/config"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/core"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/harvesterhci"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/healthcheck"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/kubevirt"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/longhorn"
	ctlhealthv1 "github.com/harvester/resource-health-controller/pkg/generated/controllers/health.harvesterhci.io/v1beta1"
)

// registrations is the registry of checks, grouped by API group; a new check only needs to be added here.
var registrations = []healthcheck.Registration{
	core.RegisterNodeCheck,
	kubevirt.RegisterVMICheck,
	longhorn.RegisterVolumeCheck,
	harvesterhci.RegisterVMBackupCheck,
	harvesterhci.RegisterScheduleVMBackupCheck,
	harvesterhci.RegisterVMImageCheck,
	harvesterhci.RegisterUpgradeCheck,
	harvesterhci.RegisterAddonCheck,
	harvesterhci.RegisterSupportBundleCheck,
	harvesterhci.RegisterUpgradeLogCheck,
	harvesterhci.RegisterVolumeRemoteBackupCheck,
	harvesterhci.RegisterVolumeRemoteRestoreCheck,
}

// Handler dispatches ComponentHealth keys to the check that owns them.
type Handler struct {
	componentHealthController ctlhealthv1.ComponentHealthController
	healthCachesSynced        []cache.InformerSynced
	checks                    []healthcheck.Check
}

func Register(ctx context.Context, management *config.Management) error {
	componentHealths := management.HealthFactory.Health().V1beta1().ComponentHealth()
	ignoreRules := management.HealthFactory.Health().V1beta1().HealthIgnoreRule()

	writer := healthcheck.Writer{
		Controller:  componentHealths,
		Client:      componentHealths,
		IgnoreRules: ignoreRules.Cache(),
	}
	h := &Handler{
		componentHealthController: componentHealths,
		healthCachesSynced:        []cache.InformerSynced{ignoreRules.Informer().HasSynced},
	}

	// resource handlers only enqueue the ComponentHealth key, the workqueue de-duplicates them
	for _, register := range registrations {
		check, synced := register(ctx, writer, management)
		h.checks = append(h.checks, check)
		h.healthCachesSynced = append(h.healthCachesSynced, synced)
	}
	ignoreRules.OnChange(ctx, ignoreRuleControllerName, h.OnIgnoreRuleChanged)

	// the only place checks are computed: dispatch by ComponentHealth key, registered once h.checks is complete
	componentHealths.OnChange(ctx, "component-health-reconcile", h.OnComponentHealthChanged)

	// seed every key once caches are synced so checks exist even when no resource changes
	go h.enqueueInitialHealth(ctx)

	return nil
}

func (h *Handler) enqueueHealth(name string) {
	h.componentHealthController.Enqueue(name)
}

func (h *Handler) enqueueInitialHealth(ctx context.Context) {
	if !cache.WaitForCacheSync(ctx.Done(), h.healthCachesSynced...) || ctx.Err() != nil {
		return
	}
	h.enqueueAllHealth()
}

func (h *Handler) enqueueAllHealth() {
	for _, check := range h.checks {
		h.enqueueHealth(check.Name())
	}
}

func (h *Handler) OnComponentHealthChanged(key string, health *healthv1.ComponentHealth) (*healthv1.ComponentHealth, error) {
	for _, check := range h.checks {
		if check.Name() != key {
			continue
		}
		for _, synced := range h.healthCachesSynced {
			if !synced() {
				return health, fmt.Errorf("ComponentHealth resource caches are not synced")
			}
		}
		return health, check.Reconcile()
	}
	return health, nil
}
