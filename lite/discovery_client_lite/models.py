"""Data types for discovery-client-lite."""

from dataclasses import dataclass, field
from typing import List, Optional, Tuple


@dataclass(frozen=True)
class Endpoint:
    """A discovery controller endpoint from config."""

    traddr: str
    port: str = '8009'
    hostnqn: str = ''
    subnqn: str = ''
    secret: str = ''
    ctrl_secret: str = ''  # DH-CHAP controller secret
    hostid: str = ''
    ctrl_loss_tmo: Optional[int] = None  # per-endpoint override


@dataclass
class CachedReferral:
    """A discovered referral with a timestamp for expiry."""

    traddr: str
    port: str
    subnqn: str
    discovered_at: float = 0.0

    def to_key(self) -> Tuple[str, str, str]:
        return (self.traddr, self.port, self.subnqn)


@dataclass
class ConnectedController:
    """An NVMe controller visible in sysfs."""

    device: str
    traddr: str
    port: str
    subnqn: str
    hostnqn: str
    transport: str


RECONNECT_GRACE_MULTIPLIER = 3  # wait this many reconnect_delay intervals before failover


@dataclass
class ClusterState:
    """State for a single cluster (identified by subnqn)."""

    subnqn: str
    active_endpoint: Optional[Tuple[str, str]] = None
    endpoints: List[Endpoint] = field(default_factory=list)
    referrals: List[CachedReferral] = field(default_factory=list)
    reconnecting_since: Optional[float] = None  # timestamp when active endpoint disappeared
