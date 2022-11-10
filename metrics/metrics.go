// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import "github.com/prometheus/client_golang/prometheus"

// DiscoveryClientMetrics a collection of metrics our application will expose
type DiscoveryClientMetrics struct {
	// Connections connections to different discovery servers
	Connections     *prometheus.GaugeVec
	// ConnectionState - whether a connection is connected to discovery target or not
	ConnectionState *prometheus.GaugeVec
	// EntriesTotal - entry count we monitor
	EntriesTotal *prometheus.GaugeVec
	// FileEntries - entry count per file
	FileEntries *prometheus.GaugeVec
	// DiscoveryLogPageCount - count how much log pages we got for each hostnqn
	DiscoveryLogPageCount *prometheus.GaugeVec
}

var Metrics DiscoveryClientMetrics

func init() {
	Metrics.Connections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_connections_total",
			Help: "Number of connections to different discovery servers",
		},
		[]string{"trtype", "traddr", "trsvcid", "nqn", "hostnqn"},
	)
	Metrics.ConnectionState = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "discovery_connection_state",
            Help: "Show if a connection to discovery service is working or not",
        },
        []string{"trtype", "traddr", "trsvcid", "nqn"},
    )
	Metrics.DiscoveryLogPageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_log_page_count",
			Help: "Number of discovery log pages for hostnqn",
		},
		[]string{"hostnqn"},
	)
	Metrics.EntriesTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_entries_total",
			Help: "Number of entries we monitor",
		},
		[]string{},
	)

	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(Metrics.Connections)
	prometheus.MustRegister(Metrics.ConnectionState)
	prometheus.MustRegister(Metrics.EntriesTotal)
	prometheus.MustRegister(Metrics.DiscoveryLogPageCount)
}
