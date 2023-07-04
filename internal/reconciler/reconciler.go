// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package reconciler implements a reconciler for reconciling Service resources.
package reconciler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClientProvider is an interface for providing a Kubernetes client.
type ClientProvider interface {
	GetClient() client.Client
}

// ServiceHandler is an interface for handling Service resources.
type ServiceHandler interface {
	Handle(svc *corev1.Service) error
	HandleDelete(svcName string) error
}

var _ reconcile.Reconciler = &Reconciler{}

// Reconciler reconciles Service resources.
//
// Implements reconcile.Reconciler.
type Reconciler struct {
	serviceHandler ServiceHandler
	clientProvider ClientProvider
}

// New returns a new Reconciler.
func New(clientProvider ClientProvider, serviceHandler ServiceHandler) (*Reconciler, error) {
	if serviceHandler == nil {
		return nil, fmt.Errorf("serviceHandler must not be nil")
	}

	if clientProvider == nil {
		return nil, fmt.Errorf("clientProvider must not be nil")
	}

	return &Reconciler{
		serviceHandler: serviceHandler,
		clientProvider: clientProvider,
	}, nil
}

// Reconcile implements reconcile.Reconciler.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	svc := &corev1.Service{}
	svcName := request.Name + "." + request.Namespace

	err := r.clientProvider.GetClient().Get(ctx, request.NamespacedName, svc)
	if errors.IsNotFound(err) {
		if err = r.serviceHandler.HandleDelete(svcName); err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	if err != nil {
		return reconcile.Result{}, fmt.Errorf("could not fetch Service: %w", err)
	}

	if err = r.serviceHandler.Handle(svc); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to handle Service: %w", err)
	}

	return reconcile.Result{}, nil
}
