/*
Copyright (c) 2018 Red Hat, Inc.

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

package main

import (
	"fmt"
	v1alpha1 "github.com/openshift/cluster-operator/pkg/apis/clusteroperator/v1alpha1"
	"github.com/openshift/cluster-operator/pkg/client/clientset_generated/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// ClusterProvisioner is the interface used by cluster service to
// provision clusters.
type ClusterProvisioner interface {
	Provision(uuid string) error
}

// ClusterOperatorProvisioner is the struct implementing ClusterProvisioner
// using Cluster Operator.
type ClusterOperatorProvisioner struct {
	k8sConfig *rest.Config
}

// Provision provisions a cluster on aws using cluster operator.
func (provisioner ClusterOperatorProvisioner) Provision(uuid string) error {
	clusterSpec := v1alpha1.ClusterSpec{
		ClusterVersionRef: v1alpha1.ClusterVersionReference{
			Name: "v1alpha1",
		},
	}
	clusterDeployment := v1alpha1.ClusterDeployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1alpha1",
			Kind:       "ClusterDeployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("cluster-%s", uuid),
		},
		Spec: clusterSpec,
	}

	clusterOperatorClient, err := clientset.NewForConfig(provisioner.k8sConfig)
	if err != nil {
		return fmt.Errorf("Failed to create kubernetes client: %s", err)
	}
	// Create the cluster deployment custom resource
	clusterOperatorClient.ClusteroperatorV1alpha1().
		ClusterDeployments("dedicated-portal").
		Create(&clusterDeployment)
	return nil
}

//NewClusterOperatorProvisioner A constructor for ClusterOperatorProvisioner struct.
func NewClusterOperatorProvisioner(k8sConfig *rest.Config) ClusterOperatorProvisioner {
	return ClusterOperatorProvisioner{
		k8sConfig: k8sConfig,
	}
}
