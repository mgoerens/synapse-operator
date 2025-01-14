/*
Copyright 2021.

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

package heisenbridge

import (
	"context"
	"reflect"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opdev/subreconciler"
	synapsev1alpha1 "github.com/opdev/synapse-operator/apis/synapse/v1alpha1"
	"github.com/opdev/synapse-operator/helpers/utils"
)

// HeisenbridgeReconciler reconciles a Heisenbridge object
type HeisenbridgeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func GetHeisenbridgeServiceFQDN(h synapsev1alpha1.Heisenbridge) string {
	return strings.Join([]string{h.Name, h.Namespace, "svc", "cluster", "local"}, ".")
}

//+kubebuilder:rbac:groups=synapse.opdev.io,resources=heisenbridges,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=synapse.opdev.io,resources=heisenbridges/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=synapse.opdev.io,resources=heisenbridges/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Heisenbridge object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *HeisenbridgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var h synapsev1alpha1.Heisenbridge // The Heisenbridge object being reconciled
	if r, err := r.getLatestHeisenbridge(ctx, req, &h); subreconciler.ShouldHaltOrRequeue(r, err) {
		return subreconciler.Evaluate(r, err)
	}

	// The list of subreconcilers for Heisenbridge.
	var subreconcilersForHeisenbridge []subreconciler.FnWithRequest

	// We need to trigger a Synapse reconciliation so that it becomes aware of
	// the Heisenbridge.
	subreconcilersForHeisenbridge = []subreconciler.FnWithRequest{
		r.triggerSynapseReconciliation,
	}

	// The user may specify a ConfigMap, containing the heisenbridge.yaml
	// config file, under Spec.Bridges.Heisenbridge.ConfigMap
	if h.Spec.ConfigMap.Name != "" {
		// If the user provided a custom Heisenbridge configuration via a
		// ConfigMap, we need to validate that the ConfigMap exists, and
		// create a copy. We also need to edit the heisenbridge
		// configuration.
		subreconcilersForHeisenbridge = append(
			subreconcilersForHeisenbridge,
			r.copyInputHeisenbridgeConfigMap,
			r.configureHeisenbridgeConfigMap,
		)
	} else {
		// If the user hasn't provided a ConfigMap with a custom
		// heisenbridge.yaml, we create a new ConfigMap with a default
		// heisenbridge.yaml.
		subreconcilersForHeisenbridge = append(
			subreconcilersForHeisenbridge,
			r.reconcileHeisenbridgeConfigMap,
		)
	}

	// Reconcile Heisenbridge resources: Service and Deployment
	subreconcilersForHeisenbridge = append(
		subreconcilersForHeisenbridge,
		r.reconcileHeisenbridgeService,
		r.reconcileHeisenbridgeDeployment,
	)

	// Run all subreconcilers sequentially
	for _, f := range subreconcilersForHeisenbridge {
		if r, err := f(ctx, req); subreconciler.ShouldHaltOrRequeue(r, err) {
			return subreconciler.Evaluate(r, err)
		}
	}

	return subreconciler.Evaluate(subreconciler.DoNotRequeue())
}

func (r *HeisenbridgeReconciler) getLatestHeisenbridge(
	ctx context.Context,
	req ctrl.Request,
	h *synapsev1alpha1.Heisenbridge,
) (*ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if err := r.Get(ctx, req.NamespacedName, h); err != nil {
		if k8serrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			log.Error(
				err,
				"Cannot find Heisenbridge - has it been deleted ?",
				"Heisenbridge Name", h.Name,
				"Heisenbridge Namespace", h.Namespace,
			)
			return subreconciler.DoNotRequeue()
		}
		log.Error(
			err,
			"Error fetching Heisenbridge",
			"Heisenbridge Name", h.Name,
			"Heisenbridge Namespace", h.Namespace,
		)
		return subreconciler.RequeueWithError(err)
	}

	return subreconciler.ContinueReconciling()
}

func (r *HeisenbridgeReconciler) fetchSynapseInstance(
	ctx context.Context,
	h synapsev1alpha1.Heisenbridge,
	s *synapsev1alpha1.Synapse,
) error {
	// Validate Synapse instance exists
	keyForSynapse := types.NamespacedName{
		Name:      h.Spec.Synapse.Name,
		Namespace: utils.ComputeNamespace(h.Namespace, h.Spec.Synapse.Namespace),
	}
	return r.Get(ctx, keyForSynapse, s)
}

func (r *HeisenbridgeReconciler) triggerSynapseReconciliation(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	h := &synapsev1alpha1.Heisenbridge{}
	if r, err := r.getLatestHeisenbridge(ctx, req, h); subreconciler.ShouldHaltOrRequeue(r, err) {
		return r, err
	}

	s := synapsev1alpha1.Synapse{}
	if err := r.fetchSynapseInstance(ctx, *h, &s); err != nil {
		log.Error(err, "Error getting Synapse instance")
		return subreconciler.RequeueWithError(err)
	}

	s.Status.NeedsReconcile = true

	if err := utils.UpdateSynapseStatus(ctx, r.Client, &s); err != nil {
		return subreconciler.RequeueWithError(err)
	}

	return subreconciler.ContinueReconciling()
}

func (r *HeisenbridgeReconciler) setFailedState(ctx context.Context, h *synapsev1alpha1.Heisenbridge, reason string) error {
	h.Status.State = "FAILED"
	h.Status.Reason = reason

	return r.updateHeisenbridgeStatus(ctx, h)
}

func (r *HeisenbridgeReconciler) updateHeisenbridgeStatus(ctx context.Context, h *synapsev1alpha1.Heisenbridge) error {
	current := &synapsev1alpha1.Heisenbridge{}
	if err := r.Get(
		ctx,
		types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
		current,
	); err != nil {
		return err
	}

	if !reflect.DeepEqual(h.Status, current.Status) {
		if err := r.Status().Patch(ctx, h, client.MergeFrom(current)); err != nil {
			return err
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HeisenbridgeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&synapsev1alpha1.Heisenbridge{}).
		Complete(r)
}
