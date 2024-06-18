package suites

import (
	"context"
	"fmt"
	"github.com/Azure/aks-app-routing-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/wait"
	"time"

	"github.com/Azure/aks-app-routing-operator/testing/e2e/logger"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func waitForAvailable(ctx context.Context, c client.Client, deployment appsv1.Deployment) error {
	lgr := logger.FromContext(ctx).With("deployment", deployment.Name, "namespace", deployment.Namespace)
	lgr.Info("waiting for deployment to be available")
	start := time.Now()
	for {
		d := &deployment
		if err := c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
			return fmt.Errorf("getting deployment: %w", err)
		}

		for _, condition := range d.Status.Conditions {
			if condition.Type == appsv1.DeploymentAvailable && condition.Status == "True" {
				lgr.Info("deployment is available")
				return nil
			}
		}

		// 20 minutes because it takes a decent amount of time for dns to "propagate", and up to 30 min for Azure RBAC to propagate for ExternalDNS to read the DNS zones
		if time.Since(start) > 20*time.Minute {
			return fmt.Errorf("timed out waiting for deployment to be available")
		}

		lgr.Info("deployment is not available yet, waiting 5 seconds for retry")
		time.Sleep(5 * time.Second)
	}
}

func waitForNICAvailable(ctx context.Context, c client.Client, nic *v1alpha1.NginxIngressController) error {
	lgr := logger.FromContext(ctx)
	lgr.Info("waiting for NIC to be available")

	testNIC := &v1alpha1.NginxIngressController{}

	if err := wait.PollImmediate(1*time.Second, 1*time.Minute, func() (bool, error) {
		lgr.Info("checking if NIC is available")
		if err := c.Get(ctx, client.ObjectKeyFromObject(nic), testNIC); err != nil {
			return false, fmt.Errorf("get nic: %w", err)
		}

		for _, cond := range nic.Status.Conditions {
			if cond.Type == v1alpha1.ConditionTypeAvailable {
				lgr.Info("found nic")
				if len(nic.Status.ManagedResourceRefs) == 0 {
					lgr.Info("nic has no ManagedResourceRefs")
					return false, nil
				}
				return true, nil
			}
		}
		lgr.Info("nic not available")
		return false, nil
	}); err != nil {
		return fmt.Errorf("waiting for test NIC to be available: %w", err)
	}

	return nil
}

func upsert(ctx context.Context, c client.Client, obj client.Object) error {
	copy := obj.DeepCopyObject().(client.Object)
	lgr := logger.FromContext(ctx).With("object", copy.GetName(), "namespace", copy.GetNamespace())
	lgr.Info(fmt.Sprintf("upserting object: %v", obj))

	// create or update the object
	lgr.Info(fmt.Sprintf("attempting to create object: %s"), copy.GetName())
	err := c.Create(ctx, copy)
	if err == nil {
		obj.SetName(copy.GetName()) // supports objects that want to use generate name
		lgr.Info("object created")
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating object: %w", err)
	}

	lgr.Info("object already exists, attempting to update")
	if err := c.Patch(ctx, copy, client.Apply, client.ForceOwnership, client.FieldOwner("e2e-test")); err != nil {
		return fmt.Errorf("updating object: %w", err)
	}

	lgr.Info("object updated")
	return nil
}
