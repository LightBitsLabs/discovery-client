"""Prometheus metrics for discovery-client-lite."""

import logging
from http.server import HTTPServer, BaseHTTPRequestHandler
from threading import Thread
from typing import Dict

log = logging.getLogger('discovery-client-lite')


class SimpleHistogram:
    """Minimal Prometheus histogram (no external dependency).

    Uses single-bucket counting: each observe() increments only the
    smallest matching bucket. render() computes cumulative counts.
    """

    BUCKETS = (0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)

    def __init__(self, name: str, help_text: str):
        self.name = name
        self.help_text = help_text
        self.buckets = [0] * len(self.BUCKETS)
        self.total = 0.0
        self.count = 0

    def observe(self, value: float):
        self.total += value
        self.count += 1
        for i, bound in enumerate(self.BUCKETS):
            if value <= bound:
                self.buckets[i] += 1
                return
        # Value exceeds all finite buckets — only +Inf

    def render(self) -> str:
        lines = [
            f'# HELP {self.name} {self.help_text}',
            f'# TYPE {self.name} histogram',
        ]
        cumulative = 0
        for i, bound in enumerate(self.BUCKETS):
            cumulative += self.buckets[i]
            lines.append(f'{self.name}_bucket{{le="{bound}"}} {cumulative}')
        lines.append(f'{self.name}_bucket{{le="+Inf"}} {self.count}')
        lines.append(f'{self.name}_sum {self.total}')
        lines.append(f'{self.name}_count {self.count}')
        return '\n'.join(lines)


class Metrics:
    """Prometheus metrics collector matching Go discovery-client metric names."""

    def __init__(self):
        # Gauges
        self.tcp_server_serving_states = 0
        self.tcp_queues_total = 0
        self.tcp_targets_total = 0
        self.clusters_tracked = 0
        # Per-hostnqn gauge
        self.targets_per_hostnqn: Dict[str, int] = {}
        # Counters
        self.targets_map_id = 0
        self.aen_sent_total = 0
        self.poll_cycles_total = 0
        self.poll_cycle_errors_total = 0
        self.failovers_total = 0
        self.connect_attempts_total = 0
        self.connect_failures_total = 0
        # Histogram
        self.poll_cycle_duration = SimpleHistogram(
            'discovery_poll_cycle_duration_seconds',
            'Duration of poll cycle in seconds',
        )

    def render(self) -> str:
        lines = []

        def gauge(name, help_text, value):
            lines.append(f'# HELP {name} {help_text}')
            lines.append(f'# TYPE {name} gauge')
            lines.append(f'{name} {value}')

        def counter(name, help_text, value):
            lines.append(f'# HELP {name} {help_text}')
            lines.append(f'# TYPE {name} counter')
            lines.append(f'{name} {value}')

        gauge(
            'discovery_tcp_server_serving_states',
            'TCP server serving states',
            self.tcp_server_serving_states,
        )
        gauge('discovery_tcp_queues_total', 'Total open TCP queues', self.tcp_queues_total)
        gauge('discovery_tcp_targets_total', 'Total TCP targets', self.tcp_targets_total)

        # Per-hostnqn labeled gauge
        lines.append('# HELP discovery_targets_per_hostnqn_total Targets per host NQN')
        lines.append('# TYPE discovery_targets_per_hostnqn_total gauge')
        for hostnqn, count in sorted(self.targets_per_hostnqn.items()):
            lines.append(f'discovery_targets_per_hostnqn_total{{hostnqn="{hostnqn}"}} {count}')

        counter('discovery_targets_map_id', 'Current target map ID', self.targets_map_id)
        counter('discovery_aen_sent_total', 'Total AEN notifications sent', self.aen_sent_total)
        counter(
            'discovery_poll_cycles_total',
            'Total number of poll cycles executed',
            self.poll_cycles_total,
        )
        counter(
            'discovery_poll_cycle_errors_total',
            'Total poll cycle errors',
            self.poll_cycle_errors_total,
        )

        # Histogram
        lines.append(self.poll_cycle_duration.render())

        counter(
            'discovery_failovers_total',
            'Total discovery controller failovers',
            self.failovers_total,
        )
        counter(
            'discovery_connect_attempts_total',
            'Total connect-all attempts',
            self.connect_attempts_total,
        )
        counter(
            'discovery_connect_failures_total',
            'Total connect-all failures',
            self.connect_failures_total,
        )
        gauge(
            'discovery_clusters_tracked', 'Number of clusters being tracked', self.clusters_tracked
        )

        return '\n'.join(lines) + '\n'


metrics = Metrics()


class MetricsHandler(BaseHTTPRequestHandler):
    """HTTP handler for /metrics endpoint."""

    def do_GET(self):
        if self.path == '/metrics':
            body = metrics.render().encode()
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain; version=0.0.4')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        """Suppress default request logging."""
        pass


def start_metrics_server(port: int):
    """Start HTTP metrics server in a daemon thread."""
    try:
        server = HTTPServer(('0.0.0.0', port), MetricsHandler)
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        log.info('Metrics server listening on :%d/metrics', port)
    except OSError as e:
        log.warning('Failed to start metrics server on port %d: %s', port, e)
