package riders

import (
	"context"
	"fmt"

	"github.com/numaproj/numaplane/internal/common"
	"github.com/numaproj/numaplane/internal/controller/config"
	"github.com/numaproj/numaplane/internal/util/kubernetes"
	"github.com/numaproj/numaplane/internal/util/logger"
	apiv1 "github.com/numaproj/numaplane/pkg/apis/numaplane/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// definition of a Rider to apply
type Rider struct {
	// new definition
	Definition unstructured.Unstructured
	// type of change required
	RequiresProgressive bool
}

// create an annotation to preserve a hash of the Rider's metadata and spec, so that we can compare the new value to the existing value
// to know if there was a change
func WithHashAnnotation(ctx context.Context, resource unstructured.Unstructured) (unstructured.Unstructured, string, error) {
	hashVal, err := kubernetes.CalculateHash(ctx, resource)
	if err != nil {
		return unstructured.Unstructured{}, "", err
	}

	unstrucWithAnnotation := resource.DeepCopy()
	annotations := unstrucWithAnnotation.GetAnnotations()

	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[common.AnnotationKeyHash] = hashVal
	unstrucWithAnnotation.SetAnnotations(annotations)
	return *unstrucWithAnnotation, hashVal, nil
}

// get the current hash value from the annotation on the Rider resource
func GetExistingHashAnnotation(resource unstructured.Unstructured) string {
	return resource.GetAnnotations()[common.AnnotationKeyHash]
}

// update the cluster according to the desired modifications to the resources (additions, mods, deletions)
func UpdateRidersInK8S(
	ctx context.Context,
	child *unstructured.Unstructured,
	riderAdditions unstructured.UnstructuredList,
	riderModifications unstructured.UnstructuredList,
	riderDeletions unstructured.UnstructuredList,
	c client.Client) error {

	numaLogger := logger.FromContext(ctx)

	numaLogger.WithValues(
		"child", child.GetName(),
		"rider additions", riderAdditions,
		"rider modifications", riderModifications,
		"rider deletions", riderDeletions).Debug("updating riders")

	// Create new Resources
	for _, rider := range riderAdditions.Items {
		if err := prepareRiderForDeployment(ctx, &rider, child); err != nil {
			return err
		}

		if err := kubernetes.CreateResource(ctx, c, &rider); err != nil {
			if apierrors.IsAlreadyExists(err) {
				numaLogger.Warnf("rider %s already exists so updating instead of creating", rider.GetName())
				// get latest version of resource so we can get its Resource Version before we try to update
				current, err := kubernetes.GetResource(ctx, c, rider.GroupVersionKind(), k8stypes.NamespacedName{
					Namespace: rider.GetNamespace(), Name: rider.GetName()})
				if err != nil {
					return fmt.Errorf("failed to get resource %s/%s: %s", rider.GetNamespace(), rider.GetName(), err)
				}
				rider.SetResourceVersion(current.GetResourceVersion())
				if err := kubernetes.UpdateResource(ctx, c, &rider); err != nil {
					return fmt.Errorf("failed to update resource %s/%s: %s", rider.GetNamespace(), rider.GetName(), err)
				}
			} else {
				return fmt.Errorf("failed to create resource %s/%s: %s", rider.GetNamespace(), rider.GetName(), err)
			}
		}
	}

	// Update existing resources that need updating
	for _, rider := range riderModifications.Items {

		if err := prepareRiderForDeployment(ctx, &rider, child); err != nil {
			return err
		}

		if err := kubernetes.UpdateResource(ctx, c, &rider); err != nil {
			return fmt.Errorf("failed to update resource %s/%s: %s", rider.GetNamespace(), rider.GetName(), err)
		}
	}

	// Delete resources that are no longer needed
	for _, rider := range riderDeletions.Items {
		if err := kubernetes.DeleteResource(ctx, c, &rider); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete resource %s/%s: %s", rider.GetNamespace(), rider.GetName(), err)
		}
	}
	return nil
}

func prepareRiderForDeployment(ctx context.Context, rider *unstructured.Unstructured, owner *unstructured.Unstructured) error {
	var err error
	// note we must hash this before applying any other fields, so that we can correctly compare this hash to a new resource which doesn't have those fields set yet
	*rider, _, err = WithHashAnnotation(ctx, *rider)
	if err != nil {
		return err
	}
	if err := kubernetes.ApplyOwnerReference(rider, owner); err != nil {
		return err
	}
	return nil
}

// Get the Rider Resources in K8S based on the resources named in []RiderStatus
func GetRidersFromK8S(ctx context.Context, namespace string, ridersList []apiv1.RiderStatus, c client.Client) (unstructured.UnstructuredList, error) {

	numaLogger := logger.FromContext(ctx)

	// for each Rider defined in the Status, get the child and return it
	// if for some reason, it's not found, just log an error here and don't include it in the list
	existingRiders := unstructured.UnstructuredList{}
	for _, existingRider := range ridersList {
		unstruc, err := kubernetes.GetResource(ctx, c, kubernetes.MetaGVKToSchemaGVK(existingRider.GroupVersionKind), k8stypes.NamespacedName{Namespace: namespace, Name: existingRider.Name})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// if for some reason it's not found, just don't include it in the list that we return and move on
				numaLogger.WithValues("GVK", existingRider.GroupVersionKind, "Name", existingRider.Name).Warn("Existing Rider not found")
			} else {
				return existingRiders, err
			}
		} else {
			if unstruc == nil {
				// this shouldn't happen but just in case
				return existingRiders, fmt.Errorf("GetResource() returned nil Unstructured for Rider %s", existingRider.Name)
			} else {
				existingRiders.Items = append(existingRiders.Items, *unstruc)
			}
		}

	}
	return existingRiders, nil
}

// Verify that the Riders' Groups/Kinds are permitted for deployment
func VerifyRidersPermitted(riders []Rider) bool {

	// get global configmap
	globalConfig, err := config.GetConfigManagerInstance().GetConfig()
	if err != nil {
		return false
	}

	// parse permitted Riders and create resource filter
	riderRules, err := kubernetes.ParseResourceFilter(globalConfig.PermittedRiders)
	if err != nil {
		return false
	}
	resourcesFilter := &kubernetes.ResourceFilter{
		IncludedResources: riderRules,
	}

	// check that each rider is a permitted Group/Kind set in the Numplane controller config
	for _, rider := range riders {
		gvk := rider.Definition.GroupVersionKind()
		if resourcesFilter.IsExcludedResource(gvk.Group, gvk.Kind, "") {
			return false
		}
	}

	return true
}
