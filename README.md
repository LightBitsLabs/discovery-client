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
      - [Functionality](#service-functionality)
    - [Override Config Using Environment Variables](#override-config-using-environment-variables)
    - [discovery-client Information Auto-Detection](#discovery-client-information-auto-detection)
  - [Pre-built Packages](#pre-built-packages)
  - [PR checks](#pr-checks)
  - [License, Warranty, Support, and Contact Information](#license-warranty-support-and-contact-information)


The `discovery-client` is a deployable service running under
systemd. It is designed for quick deployment on compute hosts
(servers) connected to an NVMe/TCP cluster such as a Lightbits
(https://www.lightbitslabs.com) cluster. An NVMe/TCP cluster is
composed of multiple servers providing remote storage over
NVMe/TCP. Servers in a storage cluster may go up or down but compute
hosts should still remain connected to and with access to their
NVMe/TCP volumes regardless of the dynamic cluster state and their
NVMe/TCP volumes actual locations. The `discovery-client` accomplishes
this goal by:

* Maintaining an updated list of NVMe-over-Fabrics discovery
  controllers. A change of the controllers in the Lightbits cluster
  will be reflected automatically in this list.

* Discovering available nvme-over-fabrics subsystems by running
  nvme-discover commands against these discovery controllers. Discover
  commands are triggered either by an AEN (asynchronous event
  notification) received from a remote discovery controller or by a
  configuration file from the user that specifies new discovery
  endpoint(s).

* Authomatically connecting to available nvme-over-fabrics subsystems
  by running `nvme connect` commands.

## Service Consumers

The `discovery-client` serves consumers running on each compute host. For example such
consumers are:

* Upstream linux client
* k8s
* OpenStack
* etc...

## Configuration

### Service Configuration

The main configuration file is located at `/etc/discovery-client/discovery-client.yaml` by default. Example configuration:

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

- `clientConfigDir`: run-time configuration directory used to communicate with the `discovery-client`. The `discovery-client` monitors it via `inotify`. The directory is created by the `discovery-client` if it does not exist.
- `maxIOQueues`: Overrides the default number of I/O queues created by the NVMe/TCP driver. Zero value means no override (default driver value is number of cores).
- `logging`: configuration of the logging package.
- `debug`: configure options for debugging.
- `autoDetectEntries`: settings for auto-detecting discovery services from existing IO controllers (see [Discovery Service Auto Detect](#discovery-service-information-auto-detection)).

### Consumer Configuration For Discovery-Targets

Initial discovery endpoints must be provided by the consumer of the `discovery-client`. The `discovery-client` needs at least one discovery endpoint on the Lightbits cluster to connect to and discover the other discovery controllers.

The `discovery-client` is configured by the [consumers](#service-consumers) via a configuration file.

#### Configuration Directory

The file is placed under `/etc/discovery-client/discovery.d/<name>.conf`

`name` - Defined by the consumer. It must be a proper file name that does not start with "tmp.dc."

#### Configuration File Format

The file needs to be written in the same format as a `discovery.conf` file that is fed to `nvme-cli`.

For reference see [nvme-discover](https://github.com/linux-nvme/nvme-cli/blob/44755ae6869ab2a9dc6ac976fb43f4f2d746336c/Documentation/nvme-discover.txt)

> **NOTE:**
>
> There is an extra mandatory field as a requirement for `discovery-client` consumers which is the `subsysnqn`.
>
> The reason we need this extra field is to identify the subsystem (or cluster in clustering solution) the consumer wants to connect to.
> 
> Since the discovery-client can work with multiple Lightbits clusters it needs to know which discovery service belongs to which cluster.
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

Multiple consumers may each place their own files in the configuration directory.

##### Configuration File Example

Say we have a client with hostnqn `hostnqn1` connecting to a three node cluster with subsysnqn `subsysnqn1`:

* `server0` - discovery service running on `10.10.10.10:8009`
* `server1` - discovery service running on `10.10.10.11:8009`
* `server2` - discovery service running on `10.10.10.12:8009`

The consumer will create a single file `/etc/discovery-client/discovery-conf.d/endpoints.conf`:

```bash
-t tcp -a 10.10.10.10 -s 8009 -q hostnqn1 -n subsysnqn1
-t tcp -a 10.10.10.11 -s 8009 -q hostnqn1 -n subsysnqn1
-t tcp -a 10.10.10.12 -s 8009 -q hostnqn1 -n subsysnqn1
```

##### Configuration File Creation By Consumers

In order to monitor configuration changes initiated by the consumer, `discovery-client` utilizes `ifnotify` functionality on the [`clientConfigDir`](#configuration-directory).

This means that the consumer needs to create the file atomically.

In order to ensure atomicity the consumer can either:

* Use atomic operations like 'mv' supported by Linux Posix file system: First write a temporary file in a temporary directory and only then move it to [`clientConfigDir`](#configuration-directory)
* Use discovery client [cli command](#Subcommands) to configure the file

##### Subcommand to create configuration files

In order to provide an easy way to configure the `discovery-client` two subcommands are provided:

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

#### Functionality

The `discovery-client` maintains a list of discovery endpoints. It uses these endpoints to discover available nvme-over-fabrics subsystems.
Persistency of this list is ensured by writing it to a json file after every update.

How is this list populated?

Endpoints may be added to this list in two ways:
* Addition of a new configuration file (or updating an existing one) as described above.
* By discovering a new endpoint through issuing a discover command on existing endpoints.

An endpoint will be removed from the list in two cases:
* A discover command did not return a log page entry (i.e. "referral") corresponding to this endpoint.
* The `discovery-client` service has restarted and found that [`clientConfigDir`] has changed after the last update of the persistent json file it maintains. In this case the discovery client will disregard the json file and will start to populate the endpoints list from scratch.

How is the list of discovery endpoints used?

* Discovery client opens a persistent TCP/IP connection to every endpoint in the list.
* It performs `nvme-discover` on each endpoint
* It performs `nvme-connect` to every controller it receives through the discover command
* It updates its internal endpoints list based on discovery endpoints ("referrals") obtained through the discovery
* It listens to AEN notifications obtained through the persistent TCP/IP connections it maintains with the discovery endpoints. Upon receiving notifications further discoveries are performed.

Monitor [`clientConfigDir`](#configuration-directory), on file Create, construct a list of discovery controllers and hostnqn it should connect to.

The `discovery-client` ignores (dedups) duplicate endpoints coming from different sources.

**NOTE:**

The `discovery-client` will not take any action to remove stale controllers.

In case that we have a connection to a server which changed IP and we run `connect-all` we will end ip with an old
`/dev/nvmeX` device with the old server connection that will not be erased.

Since the kernel uses the same device for a single `hostnqn` on a host, it is conceivable that the `discovery-client`
created `/dev/nvmeX` and an admin is manually using it by running `nvme connect` on the same `hostnqn`. In case the `discovery-client` will disconnect the device it will be lost for all other applications as well. It is recommended that on compute hosts using the `discovery-client` all NVMe/TCP manipulation (e.g., `nvme connect`) will be done through the `discovery-client`.

### Override Config Using Environment Variables

We enable overriding fields in the configuration file using environment variables.

Each env-var should follow the following pattern:

* start with `DC_` - initials of the service in question.
* uppercase variable name - for example var `foo` will become `DC_FOO`
* nested variables can be set with extra `_` for example: `foo.bar` will become `DC_FOO_BAR`.

for example in order to override the dest file of auto-detect and the port number we can run:

```bash
DC_AUTODETECTENTRIES_FILENAME=my-name DC_AUTODETECTENTRIES_DISCOVERYSERVICEPORT=12345 discovery-client ...
```

This will result in writing the outcome to `/etc/discovery-client/discovery.d/my-name`
and each entry will get the port `12345` instead of default `8009`

### discovery-client Information Auto-Detection

The `discovery-client` might encounter a problem when it has IO controllers connected already but its user-defined configuration and internal cache is deleted.

In this case when it is rebooted, no information about discovery services is available to the `discovery-client`, hence it will not connect to any discovery service, and will not handle any notifications.

On startup `discovery-client` will check if both user-defined and internal cache are empty.
If one of them is not empty, it will do nothing.

If both are empty, it will try to deduce from existing IO controllers, where are the discovery services.

Using this information, if present it will create a file and place it under user-defined folder.

Then it will start normally, pick up this file, parse it and will try to connect to discovery services described in this file.

Once it will reach at least one discovery service it will receive referral information from it, and will populate the cache with all required information.

## Authors

The `discovery-client` was written by Yogev Cohen and the rest of the Lightbits Labs development team and is copyrighted by Lightbits Labs.

## Pre-built Packages

To install the latest pre-built `.deb` package:
```bash
apt-get install -y debian-keyring  # debian only
apt-get install -y debian-archive-keyring  # debian only
apt-get install -y apt-transport-https
# If using Debian Jessie, Ubuntu 15.10 and earlier
keyring_location=/etc/apt/trusted.gpg.d/lightbits-discovery-client.gpg
# If using Debian Stretch, Ubuntu 16.04 and later
keyring_location=/usr/share/keyrings/lightbits-discovery-client-archive-keyring.gpg
curl -1sLf 'https://dl.lightbitslabs.com/public/discovery-client/gpg.014E5C7FAFD89AEE.key' |  gpg --dearmor > ${keyring_location}
distro=ubuntu # change as appropriate for your distro
codename=xenial # change as appropriate for your release
curl -1sLf "https://dl.lightbitslabs.com/public/discovery-client/config.deb.txt?distro=${distro}&codename=${codename}" > /etc/apt/sources.list.d/lightbits-discovery-client.list
apt-get update
apt-get install discovery-client
```

To install the latest pre-built `.rpm` package:
```bash
yum install yum-utils pygpgme
rpm --import 'https://dl.lightbitslabs.com/public/discovery-client/gpg.014E5C7FAFD89AEE.key'
distro=el # change as appropriate for your distro
codename=7 # change as appropriate for your release
curl -1sLf "https://dl.lightbitslabs.com/public/discovery-client/config.rpm.txt?distro=${distro}&codename=${codename}" > /tmp/lightbits-discovery-client.repo
yum-config-manager --add-repo '/tmp/lightbits-discovery-client.repo'
yum -q makecache -y --disablerepo='*' --enablerepo='lightbits-discovery-client'
yum install discovery-client
```

## PR Checks

### How-to

1. **Non-Lightbits Contributors:**
   - Fork the repository
   - Make the change you want
   - Open a pull request with the public repo (not the fork) as the base
   - A committer from Lightbits will review the pull request and, if it looks fine, they'll trigger the PR check on the pull request branch
   - You'll be notified with the results of the PR check once it's done
2. **Lightbits Contributors:**
   - Check the following document for more info [here](https://github.com/LightBitsLabs/eng-docs/blob/main/pr-checks/public-repository-pr-checks.md)

By following the steps above, you can ensure that, if the pull request you open is legitimate and up to our standards, that the PR checks run on your pull request.

## License, Warranty, Support, and Contact Information

The `discovery-client` is open source and available under the terms of
the Apache License version 2.0.

If you received the `discovery-client` from Lightbits Labs, you are
covered under the terms of your license and support agreement provided
by Lightbits. Contact info (at) lightbitslabs.com for any questions or
support. In all other cases, the following disclaimer applies:

THIS REPOSITORY IS PROVIDED BY LIGHTBITS "AS IS" AND ANY EXPRESS OR
IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL LIGHTBITS BE LIABLE FOR ANY DIRECT,
INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
(INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES, LOSS OF USE, LOSS AND/OR CORRUPTION OF DATA, LOST PROFITS,
OR BUSINESS INTERRUPTION) OR ANY OTHER LOSS HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS REPOSITORY, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH
DAMAGE.
