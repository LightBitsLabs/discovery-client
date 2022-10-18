package metrics

import "github.com/prometheus/client_golang/prometheus"

// AppMetrics a collection of metrics our application will expose
type AppMetrics struct {
	// TCPServingStatus show if the TCP server is running or stopped
	TCPServingStatus *prometheus.GaugeVec
	// TCPQueues shows how many open TCP queues we currently have
	TCPQueues *prometheus.GaugeVec
	// TargetsPerHostNqnTotal shows the Map ID we expose currently.
	TargetMapID *prometheus.CounterVec
	// TargetsPerHostNqnTotal shows for each hostnqn how many targets it will be connected to
	TargetsPerHostNqnTotal *prometheus.GaugeVec
	// TargetCount shows the Map ID we expose currently.
	TargetCount *prometheus.GaugeVec
	// AENSentTotal count the number of AEN we sent per hostNqn.
	AENSentTotal *prometheus.CounterVec

	// ClusterVolumeUpdatedEventsTotal shows the number of events we got from ETCD about cluster volume.
	ClusterVolumeUpdatedEventsTotal *prometheus.CounterVec
	// ProtectionGroupUpdatedEventsTotal shows the number of events we got from ETCD about protection-groups.
	ProtectionGroupUpdatedEventsTotal *prometheus.CounterVec
	// NodeInfoUpdatedEventsTotal shows the number of events we got from ETCD about node-info.
	NodeInfoUpdatedEventsTotal *prometheus.CounterVec
	// ServerEndpointDiscoveryUpdatedEventsTotal shows the number of events we got from ETCD about discovery service endpoint.
	ServerEndpointDiscoveryUpdatedEventsTotal *prometheus.CounterVec

	// GenerateTargetStateDurationSeconds Time it took to generate new targets-state upon change.
	GenerateTargetStateDurationSeconds *prometheus.HistogramVec
	// UpdateTargetStateDurationSeconds Time it took to update new targets-state upon change.
	UpdateTargetStateDurationSeconds *prometheus.HistogramVec
	// SendAENDurationSeconds time it took to send AEN event on a specific hostnqn
	SendAENDurationSeconds *prometheus.HistogramVec
}

var Metrics AppMetrics

func init() {
	Metrics.TCPServingStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_tcp_server_serving_states",
			Help: "Shows rather TCP server is currently serving or not serving",
		},
		[]string{"id"},
	)
	Metrics.TCPQueues = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_tcp_queues_total",
			Help: "Number of TCP queues open.",
		},
		[]string{"id", "local_addr", "remote_addr"},
	)
	Metrics.TargetMapID = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_targets_map_id",
			Help: "ID of target map we currently expose.",
		},
		[]string{"id"},
	)
	Metrics.TargetsPerHostNqnTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_targets_per_hostnqn_total",
			Help: "Number of targets this hostnqn will be connected to.",
		},
		[]string{"id", "hostnqn"},
	)
	Metrics.TargetCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "discovery_tcp_targets_total",
			Help: "Number of targets have have in target map.",
		},
		[]string{"id"},
	)
	Metrics.AENSentTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_aen_sent_total",
			Help: "Number of AEN we sent per hostNqn.",
		},
		[]string{"id", "hostnqn"},
	)

	Metrics.ClusterVolumeUpdatedEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_cluster_volume_updated_events_total",
			Help: "Number of events we got from ETCD about cluster volume.",
		},
		[]string{"id"},
	)
	Metrics.ProtectionGroupUpdatedEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_pg_updated_events_total",
			Help: "Number of events we got from ETCD about protection groups.",
		},
		[]string{"id"},
	)
	Metrics.NodeInfoUpdatedEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_node_info_updated_events_total",
			Help: "Number of events we got from ETCD about node-info.",
		},
		[]string{"id"},
	)
	Metrics.ServerEndpointDiscoveryUpdatedEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "discovery_server_endpoint_updated_events_total",
			Help: "Number of events we got from ETCD about discovery service endpoints.",
		},
		[]string{"id"},
	)
	Metrics.GenerateTargetStateDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "discovery",
			Name:      "generate_target_state_duration_seconds",
			Help:      "Time it took to generate new targets-state upon change.",
		},
		[]string{"id"},
	)
	Metrics.UpdateTargetStateDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "discovery",
			Name:      "update_target_state_duration_seconds",
			Help:      "Time it took to update new targets-state upon change.",
		},
		[]string{"id"},
	)
	Metrics.SendAENDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "discovery",
			Name:      "send_aen_duration_seconds",
			Help:      "Time it took to send AEN notification to specific hostnqn.",
		},
		[]string{"id", "hostnqn"},
	)

	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(Metrics.TCPServingStatus)
	prometheus.MustRegister(Metrics.TCPQueues)
	prometheus.MustRegister(Metrics.TargetMapID)
	prometheus.MustRegister(Metrics.TargetsPerHostNqnTotal)
	prometheus.MustRegister(Metrics.TargetCount)
	prometheus.MustRegister(Metrics.AENSentTotal)

	prometheus.MustRegister(Metrics.ClusterVolumeUpdatedEventsTotal)
	prometheus.MustRegister(Metrics.ProtectionGroupUpdatedEventsTotal)
	prometheus.MustRegister(Metrics.NodeInfoUpdatedEventsTotal)
	prometheus.MustRegister(Metrics.ServerEndpointDiscoveryUpdatedEventsTotal)

	prometheus.MustRegister(*Metrics.GenerateTargetStateDurationSeconds)
	prometheus.MustRegister(*Metrics.UpdateTargetStateDurationSeconds)
	prometheus.MustRegister(*Metrics.SendAENDurationSeconds)
}
