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
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
	"github.com/segmentio/ksuid"

	"github.com/container-mgmt/dedicated-portal/pkg/api"
)

// ClustersService performs operations on clusters.
type ClustersService interface {
	List(args ListArguments) (clusters api.ClusterList, err error)
	Create(spec api.Cluster) (result api.Cluster, err error)
	Get(uuid string) (result api.Cluster, err error)
}

// GenericClustersService is a ClusterService placeholder implementation.
type GenericClustersService struct {
	connectionUrl string
	provisioner   ClusterProvisioner
}

// ListArguments are arguments relevant for listing objects.
type ListArguments struct {
	Page int
	Size int
}

// NewClustersService Creates a new ClustersService.
func NewClustersService(connectionUrl string, provisioner ClusterProvisioner) ClustersService {
	service := new(GenericClustersService)
	service.connectionUrl = connectionUrl
	service.provisioner = provisioner
	return service
}

// List returns lists of clusters.
func (cs GenericClustersService) List(args ListArguments) (result api.ClusterList, err error) {
	result.Items = make([]*api.Cluster, 0)
	db, err := sql.Open("postgres", cs.connectionUrl)
	if err != nil {
		return api.ClusterList{}, fmt.Errorf("Error openning connection: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT uuid, name
		FROM clusters
		ORDER BY uuid
		LIMIT $1
		OFFSET $2`,
		args.Size,
		args.Page*args.Size,
	)
	if err != nil {
		return api.ClusterList{}, fmt.Errorf("Error executing query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var uuid, name string
		err = rows.Scan(&uuid, &name)
		if err != nil {
			return api.ClusterList{}, err
		}
		result.Items = append(result.Items, &api.Cluster{
			Name: name,
			UUID: uuid,
		})
	}
	err = rows.Err() // get any error encountered during iteration
	if err != nil {
		return api.ClusterList{}, err
	}
	result.Page = args.Page
	result.Size = len(result.Items)
	return result, nil
}

// Create saves a new cluster definition in the Database
func (cs GenericClustersService) Create(spec api.Cluster) (result api.Cluster, err error) {
	uuid, err := ksuid.NewRandom()
	if err != nil {
		return api.Cluster{}, err
	}

	spec.UUID = uuid

	// Use cluster provisioner to Provision a cluster.
	err = cs.provisioner.Provision(spec)
	if err != nil {
		return api.Cluster{}, fmt.Errorf("An error occurred while trying	to provision cluster %s: %s",
			spec.Name, err)
	}

	db, err := sql.Open("postgres", cs.connectionUrl)
	if err != nil {
		return api.Cluster{}, err
	}
	defer db.Close()
	stmt, err := db.Prepare(`
		INSERT INTO clusters (
			uuid,
			name,
			master_nodes,
			infra_nodes,
			compute_nodes,
			memory,
			cpu_cores,
			storage
		) VALUES (
			$1,
			$2,
			$3,
			$4,
			$5,
			$6,
			$7,
			$8)
	`)
	if err != nil {
		return api.Cluster{}, err
	}
	defer stmt.Close()
	queryResult, err := stmt.Exec(
		uuid,
		spec.Name,
		spec.Nodes.Master,
		spec.Nodes.Infra,
		spec.Nodes.Compute,
		spec.Memory.Total,
		spec.CPU.Total,
		spec.Storage.Total)
	if err != nil {
		return api.Cluster{}, err
	}
	inserted, err := queryResult.RowsAffected()
	if err != nil {
		return api.Cluster{}, err
	}
	if inserted != 1 {
		return api.Cluster{}, fmt.Errorf("Error: [%d] rows inserted. 1 expected",
			inserted,
		)
	}

	totalNodes := spec.Nodes.Master + spec.Nodes.Infra + spec.Nodes.Compute
	return api.Cluster{
		Name: spec.Name,
		UUID: fmt.Sprintf("%s", uuid),
		Nodes: api.ClusterNodes{
			Total:   totalNodes,
			Master:  spec.Nodes.Master,
			Infra:   spec.Nodes.Infra,
			Compute: spec.Nodes.Compute,
		},
		Memory: api.ClusterResource{
			Total: spec.Memory.Total,
		},
		CPU: api.ClusterResource{
			Total: spec.CPU.Total,
		},
		Storage: api.ClusterResource{
			Total: spec.Storage.Total,
		},
	}, nil

}

// Get returns a single cluster by id
func (cs GenericClustersService) Get(uuid string) (result api.Cluster, err error) {
	db, err := sql.Open("postgres", cs.connectionUrl)
	if err != nil {
		return api.Cluster{}, err
	}
	defer db.Close()
	var (
		name         string
		masterNodes  int
		infraNodes   int
		computeNodes int
		memory       int
		cpuCores     int
		storage      int
	)

	err = db.QueryRow("SELECT uuid, name, master_nodes, infra_nodes, compute_nodes, memory, cpu_cores, storage FROM clusters	WHERE uuid = $1", uuid).Scan(
		&uuid, &name, &masterNodes, &infraNodes, &computeNodes, &memory, &cpuCores, &storage)
	if err != nil {
		return api.Cluster{}, err
	}
	totalNodes := masterNodes + infraNodes + computeNodes
	return api.Cluster{
			Name: name,
			UUID: uuid,
			Nodes: api.ClusterNodes{
				Total:   totalNodes,
				Master:  masterNodes,
				Infra:   infraNodes,
				Compute: computeNodes,
			},
			Memory: api.ClusterResource{
				Total: memory,
			},
			CPU: api.ClusterResource{
				Total: cpuCores,
			},
			Storage: api.ClusterResource{
				Total: storage,
			},
		},
		nil
}
