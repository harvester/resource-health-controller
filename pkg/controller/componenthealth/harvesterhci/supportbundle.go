package harvesterhci

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	ctlharvesterv1 "github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
	healthv1 "github.com/harvester/resource-health-controller/pkg/apis/health.harvesterhci.io/v1beta1"
	"github.com/harvester/resource-health-controller/pkg/config"
	"github.com/harvester/resource-health-controller/pkg/controller/componenthealth/healthcheck"
)

const (
	supportBundleControllerName      = "componentHealthSupportBundleController"
	supportBundleComponentHealthName = "harvester-controller-supportbundle"
	supportBundleKind                = "SupportBundle"

	checkKeySupportBundleFailed     = "SupportBundleFailed"
	checkKeySupportBundleGenerating = "SupportBundleGenerating"

	// supportBundleStateError is the state the support bundle controller sets when the generation fails or times out
	supportBundleStateError = "error"
	// supportBundleStateGenerating is the state while the support bundle is being collected
	supportBundleStateGenerating = "generating"
)

type supportBundleCheck struct {
	writer             healthcheck.Writer
	supportBundleCache ctlharvesterv1.SupportBundleCache
}

// NewSupportBundleCheck builds the check that reports failed SupportBundles.
func NewSupportBundleCheck(writer healthcheck.Writer, supportBundleCache ctlharvesterv1.SupportBundleCache) healthcheck.Check {
	return &supportBundleCheck{writer: writer, supportBundleCache: supportBundleCache}
}

func (c *supportBundleCheck) Name() string { return supportBundleComponentHealthName }

func (c *supportBundleCheck) OnSupportBundleChanged(_ string, bundle *harvesterv1.SupportBundle) (*harvesterv1.SupportBundle, error) {
	c.writer.Enqueue(supportBundleComponentHealthName)
	return bundle, nil
}

// RegisterSupportBundleCheck builds the SupportBundle check with its cache and registers its change handler.
func RegisterSupportBundleCheck(ctx context.Context, writer healthcheck.Writer, management *config.Management) (healthcheck.Check, cache.InformerSynced) {
	bundles := management.HarvesterFactory.Harvesterhci().V1beta1().SupportBundle()
	check := &supportBundleCheck{writer: writer, supportBundleCache: bundles.Cache()}
	bundles.OnChange(ctx, supportBundleControllerName, check.OnSupportBundleChanged)
	return check, bundles.Informer().HasSynced
}

func (c *supportBundleCheck) Reconcile() error {
	bundles, err := c.supportBundleCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list SupportBundles: %w", err)
	}

	groups := healthcheck.ResourceGroups{}
	generating := healthcheck.ResourceGroups{}
	for _, bundle := range bundles {
		if bundle.DeletionTimestamp != nil {
			continue
		}
		if bundle.Status.State == supportBundleStateError || harvesterv1.SupportBundleInitialized.IsFalse(bundle) {
			name := healthcheck.NamespacedName(bundle.Namespace, bundle.Name)
			groups.Add(checkKeySupportBundleFailed, name, supportBundleMessage(bundle, name))
		} else if bundle.Status.State == supportBundleStateGenerating {
			name := healthcheck.NamespacedName(bundle.Namespace, bundle.Name)
			generating.Add(checkKeySupportBundleGenerating, name, supportBundleMessage(bundle, name))
		}
	}

	checks := map[string]healthv1.CheckResult{}
	for key, group := range groups {
		checks[key] = healthcheck.NewCheckResult(healthcheck.CheckResultSpec{
			Severity:   healthv1.SeverityError,
			Summary:    fmt.Sprintf("%d SupportBundle(s)", len(group.Names)),
			APIVersion: harvesterv1.SchemeGroupVersion.String(),
			Kind:       supportBundleKind,
			Names:      group.Names,
			Messages:   healthcheck.SortedMessages(group.Messages),
		})
	}
	for key, group := range generating {
		checks[key] = healthcheck.NewCheckResult(healthcheck.CheckResultSpec{
			Severity:   healthv1.SeverityInfo,
			Summary:    fmt.Sprintf("%d SupportBundle(s) generating", len(group.Names)),
			APIVersion: harvesterv1.SchemeGroupVersion.String(),
			Kind:       supportBundleKind,
			Names:      group.Names,
			Messages:   healthcheck.SortedMessages(group.Messages),
		})
	}
	return c.writer.ReplaceComponentHealthChecks(supportBundleComponentHealthName, checks)
}

func supportBundleMessage(bundle *harvesterv1.SupportBundle, name string) string {
	cond := harvesterv1.SupportBundleInitialized
	return fmt.Sprintf("%s: state=%q, progress=%d, %s=%s, reason=%q, message=%q",
		name, bundle.Status.State, bundle.Status.Progress, cond, cond.GetStatus(bundle), cond.GetReason(bundle), cond.GetMessage(bundle))
}
