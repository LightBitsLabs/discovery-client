# Discovery-Client
- [Discovery-Client](#discovery-client)
  - [Service Consumers](#service-consumers)
  - [Configuration](#configuration)
    - [Service Configuration](#service-configuration)
    - [Consumer Configuration For Discovery-Targets](#consumer-configuration-for-discovery-targets)
      - [Configuration Directory](#configuration-directory)
      - [Configuration File Format](#configuration-file-format)
        - [Configuration File Example](#configuration-file-example)
        - [Configuration File Creation By Consumers](#configuration-file-creation-by-consumers)
        - [Subcommand to create configuration files](#subcommand-to-create-configuration-files)
          - [`add-hostnqn`](#add-hostnqn)
          - [`remove-hostnqn`](#remove-hostnqn)
      - [Service Functionality](#service-functionality)
    - [Override Config Using Environment Variables](#override-config-using-environment-variables)
    - [discovery-service Information Auto-Detection](#discovery-service-information-auto-detection)
  - [Kernel Open Issues](#kernel-open-issues)

A deploy-able service running under systemd.
The service provides an ongoing nvme-connect-all functionality to a remote cluster of lightOS nvme controllers. By this we mean:

* The discovery client maintains an updated list of NVMe-over-Fabrics discovery controllers. A change of the controllers in the lightOS cluster will be reflected automatically in this list.
* It discovers available nvme-over-fabrics subsystems by running nvme-discover commands against these discovery controllers. Discover commands are triggered either by an AEN (asynchronous event notification) received from a remote discovery controller or by a configuration file from the user that specifies new discovery endpoint/s.
* It automatically connects to available nvme-over-fabrics subsystems by running nvme connect commands

## Service Consumers

The service serves consumers that want to interact with the discovery-service. For example such consumers are:

* Upstream linux client
* k8s
* OpenStack
* etc...

## Configuration

### Service Configuration

As all our services there is a configuration file at `/etc/discovery-client/discovery-client.yaml`. Example configuration:

```yaml
cores: [0]
clientConfigDir: /etc/discovery-client/discovery.d/
internalDir: /etc/discovery-client/internal/
reconnectInterval: 5s
logPagePaginationEnabled: false
maxIOQueues: 0
logging:
  filename: "/var/log/discovery-client.log"
  maxAge: 96h
  maxSize: 100
  level: info
  reportCaller: true
debug:
  metrics: true
  enablepprof: true
  endpoint: 0.0.0.0:6060
autoDetectEntries:
	enabled: true
	filename: detected-io-controllers
	discoveryServicePort: 8009
```

- `clientConfigDir`: folder used to configure the service. the service will inotify this folder. created by the service if not exists.
- `maxIOQueues`: Overrides the default number of I/O queues created by the driver. Zero value means no override (default driver value is number of cores).
- `logging`: configuration of the logging package.
- `debug`: configure options for debugging the service.
- `autoDetectEntries`: settings for auto-detecting discovery-services from existing IO controllers. (see [Discovery-Service Auto Detect](#discovery-service-information-auto-detection))

### Consumer Configuration For Discovery-Targets

Initial discovery endpoints must be provided by the consumer of the service

The service will be configured by the [consumers](#service-consumers) via a configuration file.

#### Configuration Directory

The file will be placed under `/etc/discovery-client/discovery.d/<name>.conf`

`name` - Defined by the consumer. It must be a proper file name that does not start with "tmp.dc."

#### Configuration File Format

The file will be written in the format of discovery.conf that is fed to the nvme-cli.

For reference look at [nvme-discover](https://github.com/linux-nvme/nvme-cli/blob/44755ae6869ab2a9dc6ac976fb43f4f2d746336c/Documentation/nvme-discover.txt)

> **NOTE:**
>
> There is an extra mandatory field as a requirement for `discovery-client` consumers which is the `subsysnqn`.
>
> The reason we need this extra field is to identify the subsystem (or cluster in clustering solution) the consumer wants to connect to.
> 
> Since the discovery-client can work with multiple LightOS clusters it needs to know which `discovery-service` belongs to which cluster.
> This grouping is achieved by specifying the subsysnqn as an identifier for each entry.

An example input file:

```bash
# Used for extracting default parameters for discovery
#
# Example 1:
--transport=<trtype> --traddr=<traddr> --trsvcid=<trsvcid> --hostnqn=<hostnqn> --nqn=<subsysnqn>

# Example 2: (short notation)
-t <trtype> -a <traddr> -s <trsvcid> -q <hostnqn> -n <subsysnqn>
```

The `discovery-client` will not modify the file in any way.

More than one file can be supplied by the consumer. The discovery client unites all entries from all files to a single set of discovery controller endpoints without duplications.

Typically a single file will be used with multiple endpoints - one for each discovery controller endpoint.

##### Configuration File Example

Say we have a three node cluster:

* `server0` - discovery-service running on `10.10.10.10:8009`
* `server1` - discovery-service running on `10.10.10.11:8009`
* `server2` - discovery-service running on `10.10.10.12:8009`

A single volume named `volume1` with acl: [`hostnqn1`]

The consumer will create a single file `/etc/discovery-client/discovery-conf.d/endpoints.conf`:

```bash
-t tcp -a 10.10.10.10 -s 8009 -q hostnqn1 -n subsysnqn1
-t tcp -a 10.10.10.11 -s 8009 -q hostnqn1 -n subsysnqn1
-t tcp -a 10.10.10.12 -s 8009 -q hostnqn1 -n subsysnqn1
```

##### Configuration File Creation By Consumers

In order to monitor configuration changes initiated by the service consumer, discovery client utilizes `ifnotify` functionality on the [`clientConfigDir`](#configuration-directory).

This means that the consumer need to create the file atomically.

In order to ensure atomicity the consumer can either:

* Use atomic operations like 'mv' supported by Linux Posix file system: First write a temporary file in a temporary directory and only then move it to [`clientConfigDir`](#configuration-directory)
* Use discovery client [cli command](#Subcommands) to configure the file

##### Subcommand to create configuration files

In order to provide an easy way to config the service two subcommands are provided:

* `add-hostnqn` - will create a config file under `clientConfigDir`
* `remove-hostnqn` - will delete a file from that config folder `clientConfigDir`

See usage example below:

###### `add-hostnqn`

```bash
discovery-client add-hostnqn -a 192.168.16.10:8009,192.168.16.11:8009 --name v2 -n subsystem_nqn1 -q=hostnqn1
{
  "name": "/etc/discovery-client/discovery.d/v2"
}
```

The former command will create the file `/etc/discovery-client/discovery.d/v2`
containing the following content:

```bash
-t tcp -a 192.168.16.10:8009 -s 8009 -q hostnqn1 -n subsystem_nqn1
-t tcp -a 192.168.16.11:8009 -s 8009 -q hostnqn1 -n subsystem_nqn1
```

###### `remove-hostnqn`

```bash
discovery-client remove-hostnqn --name discovery_entries
{
  "name": "/etc/discovery-client/discovery.d/v2"
}
```

Will delete the file named `/etc/discovery-client/discovery.d/v2` hence indicate to the DiscoveryClient is should stop discovering this entry.


#### Service Functionality

The service maintains a list of discovery endpoints. It uses these endpoints to discover available nvme-over-fabrics subsystems.
Persistency of this list is ensured by writing it to a json file after every update.

How is this list populated?

Endpoints may be added to this list in two ways:
* Addition of a new configuration file (or updating an existing one) as described above.
* By discovering a new endpoint through issuing a discover command on existing endpoints.

An endpoint will be removed from the list in two cases:
* A discover command did not return a log page entry (i.e. "referral") corresponding to this endpoint.
* Discovery client service has restarted and found that [`clientConfigDir`] has changed after the last update of the persistent json file it maintains. In this case the discovery client will disregard the json file and will start to populate the endpoints list from scratch.

How is the list of discovery endpoints used by the service?

* Discovery client opens a persistent tcp connection with every endpoint in the list.
* It performs nvme-discover on each endpoint
* It performs nvme-connect to every controller it receives through the discover command
* It updates its internal endpoints list based on discovery endpoints ("referrals") obtained through the discovery
* It listens to AEN notifications obtained through the persistent tcp connections it maintains with the discovery endpoints. Upon receiving notifications further discoveries are performed.

Monitor [`clientConfigDir`](#configuration-directory), on file Create, construct a list of discovery controllers and hostnqn it should connect to.

The service will dedup endpoints coming from different sources.

For each entry create a discovery session by a persistent .

**NOTE:**

The `discovery-client` will not take any action to remove stale controllers.

In case that we have a connection to a server which changed IP and we run `connect-all` we will end ip with an old
`/dev/nvmeX` device with the old server connection that will not be erased.

Since the kernel uses the same device for a single `hostnqn` on a host, there might be a way that the `discovery-client`
created `/dev/nvmeX` and an Admin manually is using it by running `nvme connect` on the same `hostnqn`.
In case the `discovery-client` will disconnect the device it will be lost for all other applications as well.

### Override Config Using Environment Variables

As to all or our services, we enable overriding fields in the configuration file using env-vars.

Each env-var should follow the following pattern:

* start with `DC_` - initials of the service in question.
* uppercase variable name - for example var `foo` will become `DC_FOO`
* nested variables can be set with extra `_` for example: `foo.bar` will become `DC_FOO_BAR`.

for example in order to override the dest file of auto-detect and the port number we can run:

```bash
DC_AUTODETECTENTRIES_FILENAME=my-name DC_AUTODETECTENTRIES_DISCOVERYSERVICEPORT=12345 discovery-service serve
```

This will result in writing the outcome to `/etc/discovery-client/discovery.d/my-name`
and each entry will get the port `12345` instead of default `8009`

### discovery-service Information Auto-Detection

The service might encounter a problem when it has IO controllers connected already but it's user-defined configuration and internal cache is deleted.

In this case when it is rebooted, no information about discovery-services is available to the service, hence it will not connect to any discovery-service, and will not handle any notifications. see [LBM1-18864](https://lightbitslabs.atlassian.net/browse/LBM1-18864)

On startup `discovery-client` will check if both user-defined and internal cache are empty.
If one of them is not empty, it will do nothing.

If both are empty, it will try to deduce from existing IO controllers, where are the discovery-services.

Using this information, if present it will create a file and place it under user-defined folder.

Then the service will start normally, pick-up this file, parse it and will try to connect to discovery-services described in this file.

Once it will reach at least one `discovery-service` it will receive referral information from it, and will populate the cache with all required information.

## Kernel Open Issues

Currently there are three open issues in kernel 5.5 (should be fixed in 5.6 according to Sagi):

1. There is no dedup for nvme connections to discovery controller, meaning: each discovery call will create `sysfs` entry which is not the case for nvme connect
2. `sysfs` doesn't expose hostnqn to user space. This is necessary to manage persistent connections for data plane as well as discovery service.
3. `udev` rules does not expose hostnqn to the user-space. there is no environment variable in the ENV of the `udev`.

