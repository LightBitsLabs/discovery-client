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
