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

package synapse

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"

	subreconciler "github.com/opdev/subreconciler"
	synapsev1alpha1 "github.com/opdev/synapse-operator/apis/synapse/v1alpha1"
	"github.com/opdev/synapse-operator/helpers/reconcile"
)

// reconcileSynapseService is a function of type FnWithRequest, to be
// called in the main reconciliation loop.
//
// It reconciles the Service for synapse to its desired state.
func (r *SynapseReconciler) reconcileSynapseService(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	s := &synapsev1alpha1.Synapse{}
	if r, err := r.getLatestSynapse(ctx, req, s); subreconciler.ShouldHaltOrRequeue(r, err) {
		return r, err
	}

	objectMetaForSynapse := reconcile.SetObjectMeta(s.Name, s.Namespace, map[string]string{})

	desiredService, err := r.serviceForSynapse(s, objectMetaForSynapse)
	if err != nil {
		return subreconciler.RequeueWithError(err)
	}

	if err := reconcile.ReconcileResource(
		ctx,
		r.Client,
		desiredService,
		&corev1.Service{},
	); err != nil {
		return subreconciler.RequeueWithError(err)
	}
	return subreconciler.ContinueReconciling()
}

// serviceForSynapse returns a synapse Service object
func (r *SynapseReconciler) serviceForSynapse(s *synapsev1alpha1.Synapse, objectMeta metav1.ObjectMeta) (*corev1.Service, error) {
	service := &corev1.Service{
		ObjectMeta: objectMeta,
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:       "synapse-unsecure",
				Protocol:   corev1.ProtocolTCP,
				Port:       8008,
				TargetPort: intstr.FromInt(8008),
			}},
			Selector: labelsForSynapse(s.Name),
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
	// Set Synapse instance as the owner and controller
	if err := ctrl.SetControllerReference(s, service, r.Scheme); err != nil {
		return &corev1.Service{}, err
	}
	return service, nil
}
