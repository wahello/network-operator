/*
Copyright 2021 NVIDIA

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

package state //nolint:dupl

import (
	"strings"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/source"

	mellanoxv1alpha1 "github.com/Mellanox/network-operator/api/v1alpha1"
	"github.com/Mellanox/network-operator/pkg/consts"
	"github.com/Mellanox/network-operator/pkg/render"
	"github.com/Mellanox/network-operator/pkg/utils"
)

const (
	stateHostDeviceNetworkName        = "state-host-device-network"
	stateHostDeviceNetworkDescription = "Host Device net-attach-def CR deployed in cluster"
	resourceNamePrefix                = "nvidia.com/"
)

// NewStateHostDeviceNetwork creates a new state for HostDeviceNetwork CR
func NewStateHostDeviceNetwork(k8sAPIClient client.Client, scheme *runtime.Scheme, manifestDir string) (State, error) {
	files, err := utils.GetFilesWithSuffix(manifestDir, render.ManifestFileSuffix...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get files from manifest dir")
	}

	renderer := render.NewRenderer(files)
	return &stateHostDeviceNetwork{
		stateSkel: stateSkel{
			name:        stateHostDeviceNetworkName,
			description: stateHostDeviceNetworkDescription,
			client:      k8sAPIClient,
			scheme:      scheme,
			renderer:    renderer,
		}}, nil
}

type stateHostDeviceNetwork struct {
	stateSkel
}

type HostDeviceManifestRenderData struct {
	HostDeviceNetworkName string
	CrSpec                mellanoxv1alpha1.HostDeviceNetworkSpec
	RuntimeSpec           *runtimeSpec
	ResourceName          string
}

// Sync attempt to get the system to match the desired state which State represent.
// a sync operation must be relatively short and must not block the execution thread.
func (s *stateHostDeviceNetwork) Sync(customResource interface{}, _ InfoCatalog) (SyncState, error) {
	cr := customResource.(*mellanoxv1alpha1.HostDeviceNetwork)
	log.V(consts.LogLevelInfo).Info(
		"Sync Custom resource", "State:", s.name, "Name:", cr.Name, "Namespace:", cr.Namespace)

	objs, err := s.getManifestObjects(cr)
	if err != nil {
		return SyncStateError, errors.Wrap(err, "failed to render HostDeviceNetwork")
	}

	if len(objs) == 0 {
		return SyncStateError, errors.Wrap(err, "no rendered objects found")
	}

	netAttDef := objs[0]
	if netAttDef.GetKind() != "NetworkAttachmentDefinition" {
		return SyncStateError, errors.Wrap(err, "no NetworkAttachmentDefinition object found")
	}

	err = s.createOrUpdateObjs(func(obj *unstructured.Unstructured) error {
		if err := controllerutil.SetControllerReference(cr, obj, s.scheme); err != nil {
			return errors.Wrap(err, "failed to set controller reference for object")
		}
		return nil
	}, objs)

	if err != nil {
		return SyncStateNotReady, errors.Wrap(err, "failed to create/update objects")
	}

	// Check objects status
	syncState, err := s.getSyncState(objs)
	if err != nil {
		return SyncStateNotReady, errors.Wrap(err, "failed to get sync state")
	}

	// Get NetworkAttachmentDefinition SelfLink
	if err := s.getObj(netAttDef); err != nil {
		return SyncStateError, errors.Wrap(err, "failed to get NetworkAttachmentDefinition")
	}

	return syncState, nil
}

// Get a map of source kinds that should be watched for the state keyed by the source kind name
func (s *stateHostDeviceNetwork) GetWatchSources() map[string]*source.Kind {
	wr := make(map[string]*source.Kind)
	wr["HostDeviceNetwork"] = &source.Kind{Type: &mellanoxv1alpha1.HostDeviceNetwork{}}
	wr["NetworkAttachmentDefinition"] = &source.Kind{Type: &netattdefv1.NetworkAttachmentDefinition{}}
	return wr
}

func (s *stateHostDeviceNetwork) getManifestObjects(
	cr *mellanoxv1alpha1.HostDeviceNetwork) ([]*unstructured.Unstructured, error) {
	resourceName := cr.Spec.ResourceName
	if !strings.HasPrefix(resourceName, resourceNamePrefix) {
		resourceName = resourceNamePrefix + resourceName
	}

	renderData := &HostDeviceManifestRenderData{
		HostDeviceNetworkName: cr.Name,
		CrSpec:                cr.Spec,
		RuntimeSpec: &runtimeSpec{
			Namespace: consts.NetworkOperatorResourceNamespace,
		},
		ResourceName: resourceName,
	}

	// render objects
	log.V(consts.LogLevelDebug).Info("Rendering objects", "data:", renderData)
	objs, err := s.renderer.RenderObjects(&render.TemplatingData{Data: renderData})
	if err != nil {
		return nil, errors.Wrap(err, "failed to render objects")
	}
	log.V(consts.LogLevelDebug).Info("Rendered", "objects:", objs)
	return objs, nil
}
