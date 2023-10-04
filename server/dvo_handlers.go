/*
Copyright © 2023 Red Hat, Inc.

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

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/RedHatInsights/insights-operator-utils/responses"
	"github.com/RedHatInsights/insights-results-aggregator-mock/data"
	"github.com/RedHatInsights/insights-results-aggregator-mock/types"
)

// AllDVONamespacesResponse is a data structure that represents list of namespace
type AllDVONamespacesResponse struct {
	Status    string     `json:"status"`
	Workloads []Workload `json:"workloads"`
}

// Workload structure represents one workload entry in list of workloads
type Workload struct {
	ClusterEntry  ClusterEntry   `json:"cluster"`
	Namespace     NamespaceEntry `json:"namespace"`
	MetadataEntry MetadataEntry  `json:"metadata"`
}

// WorkloadsForCluster structure represents workload for one selected cluster
type WorkloadsForCluster struct {
	Status string `json:"status"`
}

// ClusterEntry structure contains cluster UUID and cluster name
type ClusterEntry struct {
	UUID        string `json:"uuid"`
	DisplayName string `json:"display_name"`
}

// NamespaceEntry structure contains basic information about namespace
type NamespaceEntry struct {
	UUID     string `json:"uuid"`
	FullName string `json:"name"`
}

// MetadataEntry structure contains basic information about workload metadata
type MetadataEntry struct {
	Recommendations int    `json:"recommendations"`
	Objects         int    `json:"objects"`
	ReportedAt      string `json:"reported_at"`
	LastCheckedAt   string `json:"last_checked_at"`
	HighestSeverity int    `json:"highest_severity"`
}

// DVOReport structure represents one DVO-related report
type DVOReport struct {
	Check       string `json:"check"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Remediation string `json:"remediation"`
}

// allDVONamespaces handler returns list of all DVO namespaces. Currently it
// does not depend on Organization ID as this information is passed through
// Bearer token in real Smart Proxy service. The format of output should be:
//
//		  {
//		    "status": "ok",
//		    "workloads": [
//		        {
//		            "cluster": {
//		                "uuid": "{cluster UUID}",
//		                "display_name": "{cluster UUID or displayable name}",
//		            },
//		            "namespace": {
//		                "uuid": "{namespace UUID}",
//		                "name": "{namespace real name}", // optional, might be null
//		            },
//	                 metadata": {
//	                     "recommendations": "{number of recommendations"}, // stored in DVO_REPORT table, computed as SELECT count(distinct(recommendation)) WHERE cluster="{cluster UUID}" and namespace="{namespace UUID}"
//	                     "objects": "{number of objects}",                 // stored in DVO_REPORT table, computed as SELECT count(distinct(object)) WHERE cluster="{cluster UUID}" and namespace="{namespace UUID}"
//	                     "reported_at": "{reported_at}",                   // stored in DVO_REPORT table
//	                     "last_checked_at": "{last_checked_at}",           // stored in DVO_REPORT table
//	                     "highest_severity": "{highest_severity}",         // computed with the help of Content Service
//	                 },
//	             },
//		    ]
//		}
func (server *HTTPServer) allDVONamespaces(writer http.ResponseWriter, _ *http.Request) {
	log.Info().Msg("All DVO namespaces handler")
	// set the response header
	writer.Header().Set(contentType, appJSON)

	dvoWorkloads := data.DVOWorkloads
	var workloads []Workload

	for clusterUUID, workloadsForCluster := range dvoWorkloads {
		// retrieve set of all namespaces for given cluster
		namespaces := getNamespaces(workloadsForCluster)
		// construct one workload entry
		for _, namespace := range namespaces {
			workload := Workload{
				ClusterEntry{
					UUID:        string(clusterUUID),
					DisplayName: "Cluster name " + string(clusterUUID),
				},
				NamespaceEntry{
					UUID:     namespace,
					FullName: "Namespace name " + namespace,
				},
				MetadataEntry{
					Recommendations: numberOfRecommendations(workloadsForCluster, namespace),
					Objects:         numberOfObjects(workloadsForCluster, namespace),
					ReportedAt:      time.Now().Format(time.RFC3339),
					LastCheckedAt:   time.Now().Format(time.RFC3339),
					HighestSeverity: 5,
				},
			}
			workloads = append(workloads, workload)
		}
	}

	// prepare response structure
	var responseData AllDVONamespacesResponse
	responseData.Status = "ok"
	responseData.Workloads = workloads

	// transform response structure into proper JSON payload
	bytes, err := json.MarshalIndent(responseData, "", "\t")
	if err != nil {
		log.Error().Err(err).Msg(responseDataError)
		return
	}

	// and send the response to client
	_, err = writer.Write(bytes)
	if err != nil {
		log.Error().Err(err).Msg(responseDataError)
	}
}

// dvoNamespaceForCluster implements handler for endpoint that
// returns the list of all namespaces (i.e. array of objects) to which
// this particular account has access filtered by {cluster_name}. Each
// object contains the namespace ID, the namespace display name if
// available, the cluster ID under which this namespace is created
// (repeated input), and the number of affecting recommendations for
// this namespace as well.
//
// The format of the output should be:
//
//	  {
//	    "status": "ok",
//	    "cluster": {
//	        "uuid": "{cluster UUID}",
//	        "display_name": "{cluster UUID or displayable name}",
//	    },
//	    "namespace": {
//	        "uuid": "{namespace UUID}",                       // in this case "namespace-1"
//	        "name": "{namespace real name}",                  // optional, might be null
//	    },
//	    metadata": {
//	        "recommendations": "{number of recommendations"}, // stored in DVO_REPORT table, computed as SELECT count(distinct(recommendation)) WHERE cluster="{cluster UUID}" and namespace="{namespace UUID}"
//	        "objects": "{number of objects}",                 // stored in DVO_REPORT table, computed as SELECT count(distinct(object)) WHERE cluster="{cluster UUID}" and namespace="{namespace UUID}"
//	        "reported_at": "{reported_at}",                   // stored in DVO_REPORT table
//	        "last_checked_at": "{last_checked_at}",           // stored in DVO_REPORT table
//	        "highest_severity": "{highest_severity}",         // computed with the help of Content Service
//	    },
//	    "recommendations": [                                  // list of recommendations for the namespace
//	        {
//	            "check": "{for example no_anti_affinity}",    // taken from the original full name deploment_validation_operator_no_anti_affinity
//	            "description": {description}",                // taken from Content Service
//	            "remediation": {remediation}",                // taken from Content Service
//	            "objects": [
//	                {
//	                    "kind": "{kind attribute}",           // taken from the original report, stored in JSON in DVO_REPORT_TABLE
//	                    "uid":  "{UUID}",
//	                },
//	                {
//	                    "kind": "{kind attribute}",           // taken from the original report, stored in JSON in DVO_REPORT_TABLE
//	                    "uid":  "{UUID}",
//	                }
//	             ],
//	        },
//	        {
//	            "check": "{for unset_memory_requirements}",// taken from the original full name deploment_validation_operator_no_anti_affinity
//	            "description": {description}",             // taken from Content Service
//	            "remediation": {remediation}",             // taken from Content Service
//	            "objects": [
//	            ],
//	        },
//	    ]
//	}
func (server *HTTPServer) dvoNamespaceForCluster(writer http.ResponseWriter, request *http.Request) {
	log.Info().Msg("DVO namespaces for cluster handler")
	cluster, err := getRouterParam(request, "cluster_name")
	if err != nil {
		err = responses.SendBadRequest(writer, err.Error())
		if err != nil {
			log.Error().Err(err).Msg(responseDataError)
		}
		return
	}
	log.Info().Str("cluster selector", cluster).Msg("Query parameters")

	_, err = ValidateClusterName(cluster)
	if err != nil {
		err = responses.SendBadRequest(writer, err.Error())
		if err != nil {
			log.Error().Err(err).Msg(responseDataError)
		}
		return
	}
	log.Info().Msg("Cluster name is correct")

	_, found := data.DVOWorkloads[types.ClusterName(cluster)]
	if !found {
		message := fmt.Sprintf("DVO namespaces for cluster %s not found", cluster)
		log.Info().Msg(message)
		err = responses.SendNotFound(writer, message)
		if err != nil {
			log.Error().Err(err).Msg(responseDataError)
		}
		return
	}

	// set the response header
	writer.Header().Set(contentType, appJSON)

	// prepare response structure
	var responseData WorkloadsForCluster
	responseData.Status = "ok"

	// transform response structure into proper JSON payload
	bytes, err := json.MarshalIndent(responseData, "", "\t")
	if err != nil {
		log.Error().Err(err).Msg(responseDataError)
		return
	}

	// and send the response to client
	_, err = writer.Write(bytes)
	if err != nil {
		log.Error().Err(err).Msg(responseDataError)
	}
}

// getNamespaces returns set of all namespaces, i.e. all items will be unique
func getNamespaces(workloads []types.DVOWorkload) []string {
	// set of all namespaces for given cluster
	var namespaces = make(map[string]struct{})
	for _, workload := range workloads {
		namespaces[workload.NamespaceUID] = struct{}{}
	}

	// convert map to slice of keys.
	keys := []string{}
	for key, _ := range namespaces {
		keys = append(keys, key)
	}

	return keys
}

// numberOfRecommendations computes number of recommendations for a cluster and
// namespace
func numberOfRecommendations(workloads []types.DVOWorkload, namespace string) int {
	// set of unique rules
	var rules = make(map[string]struct{})

	for _, workload := range workloads {
		// add a rule just if it is from the same namespace
		if workload.NamespaceUID == namespace {
			rules[workload.Rule] = struct{}{}
		}
	}
	return len(rules)
}

// numberOfObjects computes number of objects for a cluster and namespace
func numberOfObjects(workloads []types.DVOWorkload, namespace string) int {
	// object counter
	objects := 0

	for _, workload := range workloads {
		// count an object just if it is from the same namespace
		if workload.NamespaceUID == namespace {
			objects++
		}
	}
	return objects
}
